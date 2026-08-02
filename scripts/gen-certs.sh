#!/usr/bin/env bash
# gen-certs.sh — Generate a self-signed CA and server certificate for Greybox domains.
# Install the CA cert on client machines to trust the private server's TLS.
set -euo pipefail

CERT_DIR="$(cd "$(dirname "$0")/.." && pwd)/certs"
mkdir -p "$CERT_DIR"

# The client validates the certificate against the address it dials, and for the
# mmog/firmament sockets that address is an IP, not a hostname -- so this host's
# LAN IP has to be in the SAN or the client drops the connection. It used to be
# hardcoded to one machine's 10.0.0.73, which made the generated certificate
# useless on any other host. Override with SERVER_IP=<addr> if the guess is
# wrong (multi-homed hosts, or when clients reach you through a router).
SERVER_IP="${SERVER_IP:-$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{print $7; exit}')}"
SERVER_IP="${SERVER_IP:-127.0.0.1}"
echo "[*] Certificate will be valid for IP: $SERVER_IP (override with SERVER_IP=...)"

echo "[*] Generating CA key and certificate..."
openssl genrsa -out "$CERT_DIR/ca.key" 4096
openssl req -new -x509 -days 3650 -key "$CERT_DIR/ca.key" \
  -out "$CERT_DIR/ca.crt" \
  -subj "/C=US/ST=Private/L=Server/O=Dreadnought Private Server/CN=Dreadnought PS CA"

echo "[*] Generating server key..."
openssl genrsa -out "$CERT_DIR/server.key" 2048

echo "[*] Generating server CSR..."
openssl req -new -key "$CERT_DIR/server.key" \
  -out "$CERT_DIR/server.csr" \
  -subj "/C=US/ST=Private/L=Server/O=Dreadnought Private Server/CN=profile-api.prod.greybox.sixfoot.live"

echo "[*] Creating SAN extension file..."
cat > "$CERT_DIR/san.ext" << EOF
[SAN]
subjectAltName=DNS:profile-api.prod.greybox.sixfoot.live,DNS:legacyapi.prod.greybox.sixfoot.live,DNS:mmog.greybox.sixfoot.live,DNS:masterserver.local,DNS:gamemanager.local,DNS:localhost,DNS:firmament.prod.greybox.sixfoot.live,DNS:*.prod.greybox.sixfoot.live,DNS:*.greybox.sixfoot.live,DNS:*.sixfoot.live,IP:${SERVER_IP},IP:127.0.0.1
EOF

echo "[*] Signing server certificate with CA..."
openssl x509 -req -days 3650 \
  -in "$CERT_DIR/server.csr" \
  -CA "$CERT_DIR/ca.crt" \
  -CAkey "$CERT_DIR/ca.key" \
  -CAcreateserial \
  -out "$CERT_DIR/server.crt" \
  -extfile "$CERT_DIR/san.ext" \
  -extensions SAN

echo ""
echo "[✓] Certificates generated in $CERT_DIR"
echo ""
echo "  CA cert:     $CERT_DIR/ca.crt"
echo "  Server cert: $CERT_DIR/server.crt"
echo "  Server key:  $CERT_DIR/server.key"
echo ""
echo "[!] IMPORTANT: Install ca.crt as a trusted root CA on ALL client machines."
echo "    Windows: certmgr.msc → Trusted Root Certification Authorities → Import"
echo "    Linux:   sudo cp ca.crt /usr/local/share/ca-certificates/dn-ps.crt && sudo update-ca-certificates"

echo "[*] Generating Firmament cert with Amazon RSA 2048 M01 issuer (bypasses cert pinning)..."
# The game checks: X509_check_issued(hardcoded_amazon_ca, server_cert)
# This requires our server cert to have:
#   - issuer = CN=Amazon RSA 2048 M01, O=Amazon, C=US
#   - AKID keyid = 81:B8:0E:63:8A:89:12:18:E5:FA:3B:3B:50:95:9F:E6:E5:90:13:85 (Amazon cert SKID)
# NOTE: TLS peer verification is disabled in the game, so the signature doesn't matter.
AMAZON_SKID="81:B8:0E:63:8A:89:12:18:E5:FA:3B:3B:50:95:9F:E6:E5:90:13:85"
AMAZON_SKID_HEX="${AMAZON_SKID//:/}"
python3 - << PYEOF
import datetime, ipaddress
from cryptography import x509
from cryptography.x509.oid import NameOID
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa

AMAZON_SKID = bytes.fromhex("${AMAZON_SKID_HEX}")
key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
subject = x509.Name([
    x509.NameAttribute(NameOID.COUNTRY_NAME, "US"),
    x509.NameAttribute(NameOID.ORGANIZATION_NAME, "Greybox"),
    x509.NameAttribute(NameOID.COMMON_NAME, "firmament.greybox.aviary.cloud"),
])
issuer = x509.Name([
    x509.NameAttribute(NameOID.COUNTRY_NAME, "US"),
    x509.NameAttribute(NameOID.ORGANIZATION_NAME, "Amazon"),
    x509.NameAttribute(NameOID.COMMON_NAME, "Amazon RSA 2048 M01"),
])
now = datetime.datetime.now(datetime.timezone.utc)
cert = (x509.CertificateBuilder()
    .subject_name(subject).issuer_name(issuer)
    .public_key(key.public_key())
    .serial_number(x509.random_serial_number())
    .not_valid_before(now).not_valid_after(now + datetime.timedelta(days=3650))
    .add_extension(x509.AuthorityKeyIdentifier(key_identifier=AMAZON_SKID, authority_cert_issuer=None, authority_cert_serial_number=None), critical=False)
    .add_extension(x509.SubjectKeyIdentifier.from_public_key(key.public_key()), critical=False)
    .add_extension(x509.SubjectAlternativeName([
        # Same literal, same bug as the one fixed for the server certificate
        # above: hardcoding one machine's address made the firmament cert
        # useless anywhere else. It survived here because the client pins this
        # connection on the "Amazon RSA 2048 M01" issuer rather than checking
        # the IP SAN, so nobody hit it -- but a stricter path would.
        x509.IPAddress(ipaddress.IPv4Address("${SERVER_IP}")),
        x509.IPAddress(ipaddress.IPv4Address("127.0.0.1")),
        x509.DNSName("firmament.greybox.aviary.cloud"),
        x509.DNSName("firmament.prod.greybox.sixfoot.live"),
    ]), critical=False)
    .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
    .sign(key, hashes.SHA256()))
with open("$CERT_DIR/firmament.crt", "wb") as f:
    f.write(cert.public_bytes(serialization.Encoding.PEM))
with open("$CERT_DIR/firmament.key", "wb") as f:
    f.write(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.TraditionalOpenSSL, serialization.NoEncryption()))
print("Firmament cert created")
PYEOF

echo "[✓] Firmament cert: $CERT_DIR/firmament.crt (use with FIRMAMENT_CERT env var)"
echo "    Issuer: CN=Amazon RSA 2048 M01, O=Amazon (bypasses game's cert pinning check)"
