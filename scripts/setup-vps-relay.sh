#!/usr/bin/env bash
# setup-vps-relay.sh -- provision a public VPS as the Dreadnought relay.
#
# The home connection sits behind CGNAT (Digi), so it can only make OUTBOUND
# connections. This script therefore turns the VPS into:
#   1. a WireGuard server, with the home machine's machine as the client
#      (one outbound UDP flow home->VPS:51820; CGNAT cannot block it);
#   2. a plain port relay: game traffic arriving on the VPS's public TCP
#      65443/48843/8443/25565 (minecraft) and UDP 7777 is DNAT-ed over the
#      tunnel to the home
#      machine, which itself dials nothing. The game traffic is already TLS
#      (gateway) or pinned binary (firmament/MMOG), so the relay needs no
#      application logic.
#
#   3. the real-certificate HTTPS front for the public domain: nginx listens on
#      443 and dispatches by TLS SNI (ssl_preread) -- SNI <public-domain>
#      goes to local Caddy (real Let's Encrypt cert, LE site on :8444, ACME
#      http-01 via port 80 which is never DNAT-ed), everything else passes
#      through RAW to the home gateway, whose private-CA certificate the
#      launcher pins. That split means the launcher needs NO web_port: it dials
#      :443 with SNI profile-api.* and lands on the home gateway untouched.
#      Provisioned manually (nginx + caddy configs);
#      this script covers the WG/relay half.
#
# Idempotent: WireGuard keys live in run/vps-relay/ and are reused on re-runs;
# the firewall rules are added with -C/-A guards so a re-run repairs instead of
# duplicating. Safe to re-run after moving to a fresh VPS IP.
#
# Usage:
#   VPS_ADDR=<vps-ip> SSH_KEY=<path/to/ssh-key> \
#     [SSH_USER=ubuntu] bash scripts/setup-vps-relay.sh
#
# VPS prerequisites: Ubuntu with passwordless sudo (Oracle's default), and the
# cloud-side security list allowing TCP all (or at least 80/443/8443/65443/48843)
# and UDP 7777/51820 inbound (Oracle VCN "Security Lists" -- the OS firewall alone
# does not decide this).
set -euo pipefail

VPS_ADDR="${VPS_ADDR:?VPS_ADDR=vps-public-host-or-ip scripts/setup-vps-relay.sh}"
SSH_KEY="${SSH_KEY:?SSH_KEY=path-to-private-key scripts/setup-vps-relay.sh}"
SSH_USER="${SSH_USER:-ubuntu}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KEYDIR="$ROOT/run/vps-relay"
# WireGuard network. 10.13.37.x to stay clear of the home LAN (192.168.1.x) and
# docker's 172.17.x: a collision would make AllowedIPs swallow real LAN routes.
WGNET="${WGNET:-10.13.37}"
WG_PORT="${WG_PORT:-51820}"

command -v wg >/dev/null || { echo "wireguard-tools missing locally (pacman -S wireguard-tools)"; exit 1; }
test -r "$SSH_KEY"

mkdir -p "$KEYDIR"
for k in vps home; do
    if [ ! -s "$KEYDIR/$k.key" ]; then
        wg genkey >"$KEYDIR/$k.key"
        wg pubkey <"$KEYDIR/$k.key" >"$KEYDIR/$k.pub"
    fi
done
VPS_PRIV="$(cat "$KEYDIR/vps.key")"
VPS_PUB="$(cat "$KEYDIR/vps.pub")"
HOME_PUB="$(cat "$KEYDIR/home.pub")"

# The remote script is fed on stdin (`sudo bash -s -- args`), one quoted
# heredoc, keys and addresses passed as ARGUMENTS, never interpolated into the
# text: an unquoted heredoc would have executed $(...) or $IFS in the script
# body locally.
REMOTE='set -eu
VPS_PRIV="$1"; VPS_PUB="$2"; HOME_PUB="$3"; WGNET="$4"; WG_PORT="$5"

apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq wireguard iptables-persistent >/dev/null

printf "net.ipv4.ip_forward=1\n" > /etc/sysctl.d/90-dread-relay.conf
sysctl -p /etc/sysctl.d/90-dread-relay.conf >/dev/null

mkdir -p /etc/wireguard && chmod 700 /etc/wireguard
{
  printf "[Interface]\nAddress = %s.1/24\nListenPort = %s\nPrivateKey = %s\n\n[Peer]\n# home machine\nPublicKey = %s\nAllowedIPs = %s.2/32\n" \
    "$WGNET" "$WG_PORT" "$VPS_PRIV" "$HOME_PUB" "$WGNET"
} > /etc/wireguard/wg0.conf
chmod 600 /etc/wireguard/wg0.conf
systemctl enable --now "wg-quick@wg0" >/dev/null

WAN_IF="$(ip route show default | awk "{for(i=1;i<=NF;i++) if(\$i==\"dev\"){print \$(i+1); exit}}")"
test -n "$WAN_IF"
echo "relay detected WAN iface: $WAN_IF"
RELAY_IP="$WGNET.2"

# -- OS firewall. Oracle ships INPUT with a reject-all after the ssh accept and
# FORWARARD rejecting everything; our rules must come BEFORE those.
have() { iptables -C "$@" 2>/dev/null; }

iptables -t filter -C INPUT -p udp --dport "$WG_PORT" -j ACCEPT 2>/dev/null \
  || iptables -t filter -I INPUT 1 -p udp --dport "$WG_PORT" -j ACCEPT
# INPUT accepts are needed for anything that is NOT DNAT-ed (DNAT traffic skips
# INPUT entirely): WireGuard above, and Caddy's 80/443 below (must predate the
# ACME order or the first issuance fails on a fresh VPS).
iptables -t filter -C INPUT -p tcp -m multiport --dports 80,443 -j ACCEPT 2>/dev/null \
  || iptables -t filter -I INPUT 1 -p tcp -m multiport --dports 80,443 -j ACCEPT

# NAT: relay edges -> home over the tunnel. 80 stays local (ACME) and 443 is
# nginx's SNI dispatch (bunny front vs game TLS passthrough, so 443 does NOT
# DNAT); 8443 is kept as a direct-to-home-gateway fallback; the two game
# sockets and minecraft keep their symmetric ports.
iptables -t nat -C PREROUTING -p tcp -m multiport --dports 65443,48843 -j DNAT --to-destination "$RELAY_IP" 2>/dev/null \
  || iptables -t nat -A PREROUTING -p tcp -m multiport --dports 65443,48843 -j DNAT --to-destination "$RELAY_IP"
iptables -t nat -C PREROUTING -p tcp --dport 8443 -j DNAT --to-destination "$RELAY_IP:443" 2>/dev/null \
  || iptables -t nat -A PREROUTING -p tcp --dport 8443 -j DNAT --to-destination "$RELAY_IP:443"
# minecraft java (home server already accepts 25565)
iptables -t nat -C PREROUTING -p tcp --dport 25565 -j DNAT --to-destination "$RELAY_IP:25565" 2>/dev/null \
  || iptables -t nat -A PREROUTING -p tcp --dport 25565 -j DNAT --to-destination "$RELAY_IP:25565"
iptables -t nat -C PREROUTING -p udp --dport 7777 -j DNAT --to-destination "$RELAY_IP:7777" 2>/dev/null \
  || iptables -t nat -A PREROUTING -p udp --dport 7777 -j DNAT --to-destination "$RELAY_IP:7777"
# Replies go back through the tunnel (home answers the MASQ source), not its
# own default route -- without this the answer leaks out the home ISP and is dropped.
iptables -t nat -C POSTROUTING -o wg0 -j MASQUERADE 2>/dev/null \
  || iptables -t nat -A POSTROUTING -o wg0 -j MASQUERADE

iptables -t filter -C FORWARD -i "$WAN_IF" -o wg0 -j ACCEPT 2>/dev/null \
  || iptables -t filter -I FORWARD 1 -i "$WAN_IF" -o wg0 -j ACCEPT
iptables -t filter -C FORWARD -i wg0 -o "$WAN_IF" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null \
  || iptables -t filter -I FORWARD 1 -i wg0 -o "$WAN_IF" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT

mkdir -p /etc/iptables
iptables-save > /etc/iptables/rules.v4
printf "# ipv6 unused by the relay\n" > /etc/iptables/rules.v6
systemctl enable netfilter-persistent >/dev/null 2>&1 || true

echo "RELAY READY: wg0 up on $WGNET.1:51820, DNAT -> $RELAY_IP (tcp 8443/65443/48843/25565, udp 7777)"
echo "(80/443 belong to the local Caddy front: real-LE cert for browsers/Discord)"
wg show'

echo "[*] provisioning $VPS_ADDR ..."
ssh -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 \
    "$SSH_USER"@"$VPS_ADDR" "sudo bash -s -- '$VPS_PRIV' '$VPS_PUB' '$HOME_PUB' '$WGNET' '$WG_PORT'" <<<"$REMOTE"

echo "[*] writing $KEYDIR/wg-home.conf (the home side of the tunnel)"
umask 077
# shellcheck disable=SC2086  # deliberate word splitting for printf vars
printf '[Interface]\n# home machine, WireGuard client of the VPS relay\nAddress = %s.2/24\nPrivateKey = %s\n\n[Peer]\n# relay %s\nPublicKey = %s\nEndpoint = %s:%s\nAllowedIPs = %s.0/24\nPersistentKeepalive = 25\n' \
    "$WGNET" "$(cat "$KEYDIR/home.key")" "$VPS_ADDR" "$VPS_PUB" "$VPS_ADDR" "$WG_PORT" "$WGNET" >"$KEYDIR/wg-home.conf"
umask 022

echo
echo "[*] done. Next steps:"
echo "    sudo cp $KEYDIR/wg-home.conf /etc/wireguard/dreadvps.conf && sudo wg-quick up dreadvps"
echo "    systemctl enable wg-quick@dreadvps   # persist across reboots"
echo "    players: dn-launcher.json = {\"server\":\"<public-domain>\"} + ca.crt beside the exe"