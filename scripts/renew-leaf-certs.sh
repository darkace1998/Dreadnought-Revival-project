#!/usr/bin/env bash
# renew-leaf-certs.sh -- re-sign ONLY the leaf certificates against the EXISTING
# CA in certs/. gen-certs.sh makes a new CA on every run, which orphans every
# client that already approved the old one; this keeps the CA (and its
# fingerprint) stable. Run when the public IP or server name changes:
#
#   SERVER_IP=<public-ip> SERVER_NAME=<public-domain> bash scripts/renew-leaf-certs.sh
#
# Exports: EXTRA_IPS (comma-separated) to add more addresses, e.g. the LAN IP.
set -euo pipefail
CERT_DIR="$(cd "$(dirname "$0")/.." && pwd)/certs"
SERVER_IP="${SERVER_IP:?SERVER_IP=<outside ip> scripts/renew-leaf-certs.sh}"
SERVER_NAME="${SERVER_NAME:?SERVER_NAME=<public name> scripts/renew-leaf-certs.sh}"

LAN_IP="$(ip -4 addr show 2>/dev/null | grep -oE 'inet [0-9.]+' | head -3 | grep -v '127\.' | awk '{print $2}' | head -1 || true)"
LAN_SAN=""
LAN_PY=""
if [ -n "$LAN_IP" ] && [ "$LAN_IP" != "$SERVER_IP" ]; then
    LAN_SAN=",IP:${LAN_IP}"
    LAN_PY="x509.IPAddress(ipaddress.IPv4Address(\"${LAN_IP}\")),"
    echo "[*] also valid for LAN IP: $LAN_IP"
fi

echo "[*] renewing server.crt for IP $SERVER_IP, name $SERVER_NAME (CA unchanged)"
cat > "$CERT_DIR/san.ext" << SANEOF
[SAN]
subjectAltName=DNS:profile-api.prod.greybox.sixfoot.live,DNS:legacyapi.prod.greybox.sixfoot.live,DNS:mmog.greybox.sixfoot.live,DNS:masterserver.local,DNS:gamemanager.local,DNS:localhost,DNS:firmament.prod.greybox.sixfoot.live,DNS:*.prod.greybox.sixfoot.live,DNS:*.greybox.sixfoot.live,DNS:*.sixfoot.live,DNS:${SERVER_NAME},IP:${SERVER_IP},IP:127.0.0.1${LAN_SAN}
SANEOF
openssl x509 -req -days 3650 -in "$CERT_DIR/server.csr" \
    -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" \
    -set_serial 0x"$(openssl rand -hex 16)" \
    -out "$CERT_DIR/server.crt" \
    -extfile "$CERT_DIR/san.ext" -extensions SAN

echo "[*] renewing firmament.crt (issuer spoof, CA unchanged)"
cat > "$CERT_DIR/firmament_gen.py" << PYEOF
import datetime, ipaddress
from cryptography import x509
from cryptography.x509.oid import NameOID
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa

AMAZON_SKID = bytes.fromhex("81B80E638A891218E5FA3B3B50959FE6E5901385")
key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
subject = x509.Name([x509.NameAttribute(NameOID.COUNTRY_NAME, "US"),
    x509.NameAttribute(NameOID.ORGANIZATION_NAME, "Greybox"),
    x509.NameAttribute(NameOID.COMMON_NAME, "firmament.greybox.aviary.cloud")])
issuer = x509.Name([x509.NameAttribute(NameOID.COUNTRY_NAME, "US"),
    x509.NameAttribute(NameOID.ORGANIZATION_NAME, "Amazon"),
    x509.NameAttribute(NameOID.COMMON_NAME, "Amazon RSA 2048 M01")])
now = datetime.datetime.now(datetime.timezone.utc)
sans = [x509.IPAddress(ipaddress.IPv4Address("${SERVER_IP}")),
        x509.IPAddress(ipaddress.IPv4Address("127.0.0.1")),
        ${LAN_PY}x509.DNSName("firmament.greybox.aviary.cloud"),
        x509.DNSName("firmament.prod.greybox.sixfoot.live"),
        x509.DNSName("${SERVER_NAME}")]
cert = (x509.CertificateBuilder().subject_name(subject).issuer_name(issuer)
    .public_key(key.public_key()).serial_number(x509.random_serial_number())
    .not_valid_before(now).not_valid_after(now + datetime.timedelta(days=3650))
    .add_extension(x509.AuthorityKeyIdentifier(key_identifier=AMAZON_SKID,
        authority_cert_issuer=None, authority_cert_serial_number=None), critical=False)
    .add_extension(x509.SubjectKeyIdentifier.from_public_key(key.public_key()), critical=False)
    .add_extension(x509.SubjectAlternativeName(sans), critical=False)
    .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
    .sign(key, hashes.SHA256()))
with open("${CERT_DIR}/firmament.crt", "wb") as f:
    f.write(cert.public_bytes(serialization.Encoding.PEM))
with open("${CERT_DIR}/firmament.key", "wb") as f:
    f.write(key.private_bytes(serialization.Encoding.PEM,
        serialization.PrivateFormat.TraditionalOpenSSL, serialization.NoEncryption()))
print("Firmament cert created")
PYEOF
python3 "$CERT_DIR/firmament_gen.py"
rm -f "$CERT_DIR/firmament_gen.py"

echo "[✓] leaves renewed; CA and its fingerprint unchanged"
openssl x509 -in "$CERT_DIR/server.crt" -noout -ext subjectAltName | tail -1