"""
Revoice Controller — device-link PKI.

Generates and persists a private CA plus a server certificate for the
device WebSocket TLS listener. Files live in TLS_DIR (default: a tls/
directory next to the SQLite database, so the existing /app/data volume
mount persists them across container rebuilds):

    ca.pem / ca.key         — the Revoice controller CA (what devices pin)
    server.pem / server.key — leaf cert presented on the TLS listener

Design constraints (see CLAUDE.md "TLS device link"):

- Devices verify the chain against the pinned CA only — no system roots,
  no IP SANs. The leaf carries a fixed DNS SAN (TLS_SERVER_NAME); devices
  set the same name as tls.Config.ServerName regardless of the IP that
  mDNS handed them, so the controller can move address freely.
- Echo Dots can boot with a wildly wrong clock (no RTC battery; NTP takes
  a while after WiFi comes up). Certs are therefore backdated 10 years
  and valid for 25, and the device additionally clamps its verification
  time to the firmware build time. Do not "fix" the validity window to
  something conventional — a device that can't connect can't sync time.
- Everything is generated lazily on first use and reused forever after.
  Deleting TLS_DIR rotates the CA; devices then need a fresh credential
  push (dashboard "Secure link" action) before they can reconnect over
  TLS.
"""

from __future__ import annotations

import datetime
import ipaddress  # noqa: F401  (kept: handy in a REPL when debugging SANs)
import logging
import os
import ssl

log = logging.getLogger("revoice.pki")

# DNS name devices expect in the server cert — coupled with
# device/internal/client/tlscreds.go (tlsServerName). Not resolvable on
# purpose; it's an identity label, not an address.
TLS_SERVER_NAME = "revoice-controller"

# Names EARLIER firmware verifies against. Each one is a value some device
# in the field has compiled in as tls.Config.ServerName, and it stays here
# until no such device can exist.
#
# **This is the shape that took the fleet down, and it is worth reading
# before touching either end.** The rename changed this constant and the
# firmware's `tlsServerName` in the same commit, which is correct for a
# fresh install and catastrophic for an existing one: the SAN is baked into
# a certificate that PERSISTS in tls/, so a controller carried across the
# rename goes on presenting the old name while new firmware demands the new
# one. Verification then fails on every dial, and the device has no
# fallback — it holds a CA, mDNS advertises tls_port, so it redials wss
# forever. Measured 2026-09-11: a device on v2.27.0-fx.1 was connected over
# wss (proving the stored leaf carries the OLD name), took v2.29.0-fx.1,
# and never registered again.
#
# A certificate may carry many DNS SANs, so the fix is the same one
# em_devicepaths uses for directories: present BOTH, and let each firmware
# match the one it knows.
LEGACY_SERVER_NAMES = ("echomuse-controller",)


def server_names() -> tuple[str, ...]:
    """Every name the leaf must answer to, current first."""
    return (TLS_SERVER_NAME, *LEGACY_SERVER_NAMES)


def missing_names(present, required) -> tuple[str, ...]:
    """
    Required names the stored leaf does not carry, in `required` order.

    Pure, so the decision to re-issue is testable in an environment without
    the cryptography package — which is the controller test environment, on
    purpose.
    """
    have = set(present)
    return tuple(n for n in required if n not in have)

_NOT_BEFORE_BACKDATE = datetime.timedelta(days=3650)   # 10 years
_VALIDITY            = datetime.timedelta(days=365 * 25)


def _tls_dir(db_path: str) -> str:
    d = os.environ.get("TLS_DIR")
    if not d:
        d = os.path.join(os.path.dirname(db_path) or ".", "tls")
    return d


def _generate(tls_dir: str) -> None:
    from cryptography import x509
    from cryptography.hazmat.primitives import hashes, serialization
    from cryptography.hazmat.primitives.asymmetric import ec
    from cryptography.x509.oid import NameOID

    os.makedirs(tls_dir, exist_ok=True)
    now = datetime.datetime.now(datetime.timezone.utc)
    not_before = now - _NOT_BEFORE_BACKDATE
    not_after  = now + _VALIDITY

    def _write(path: str, data: bytes) -> None:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(data)

    # ── CA ────────────────────────────────────────────────────────────────
    ca_key  = ec.generate_private_key(ec.SECP256R1())
    ca_name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "Revoice Controller CA")])
    ca_cert = (
        x509.CertificateBuilder()
        .subject_name(ca_name)
        .issuer_name(ca_name)
        .public_key(ca_key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(not_before)
        .not_valid_after(not_after)
        .add_extension(x509.BasicConstraints(ca=True, path_length=0), critical=True)
        .add_extension(
            x509.KeyUsage(
                digital_signature=False, content_commitment=False,
                key_encipherment=False, data_encipherment=False,
                key_agreement=False, key_cert_sign=True, crl_sign=True,
                encipher_only=False, decipher_only=False,
            ),
            critical=True,
        )
        .sign(ca_key, hashes.SHA256())
    )

    # ── Server leaf ───────────────────────────────────────────────────────
    srv_key  = ec.generate_private_key(ec.SECP256R1())
    srv_cert = (
        x509.CertificateBuilder()
        .subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, TLS_SERVER_NAME)]))
        .issuer_name(ca_name)
        .public_key(srv_key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(not_before)
        .not_valid_after(not_after)
        .add_extension(
            x509.SubjectAlternativeName(
                [x509.DNSName(n) for n in server_names()]),
            critical=False,
        )
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(
            x509.ExtendedKeyUsage([x509.oid.ExtendedKeyUsageOID.SERVER_AUTH]),
            critical=False,
        )
        .sign(ca_key, hashes.SHA256())
    )

    pem = serialization.Encoding.PEM
    _write(os.path.join(tls_dir, "ca.key"), ca_key.private_bytes(
        pem, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    _write(os.path.join(tls_dir, "ca.pem"), ca_cert.public_bytes(pem))
    _write(os.path.join(tls_dir, "server.key"), srv_key.private_bytes(
        pem, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    _write(os.path.join(tls_dir, "server.pem"), srv_cert.public_bytes(pem))
    log.info(f"Generated device-link CA + server cert in {tls_dir} "
             f"(SAN={', '.join(server_names())}, valid to {not_after.date()})")


def _leaf_san_names(tls_dir: str) -> tuple[str, ...]:
    """DNS names in the stored leaf, or () if it cannot be read."""
    try:
        from cryptography import x509
    except ImportError:
        return ()
    try:
        with open(os.path.join(tls_dir, "server.pem"), "rb") as f:
            cert = x509.load_pem_x509_certificate(f.read())
        san = cert.extensions.get_extension_for_class(
            x509.SubjectAlternativeName).value
        return tuple(san.get_values_for_type(x509.DNSName))
    except Exception as e:                      # unreadable, no SAN, old shape
        log.warning(f"Could not read the SANs of the stored server cert: {e}")
        return ()


def _reissue_leaf(tls_dir: str) -> None:
    """
    Re-sign the server leaf from the EXISTING CA, covering every required
    name.

    The CA is deliberately untouched: it is what devices pin, it is already
    installed on every device in the fleet, and rotating it would mean a
    credential push to each of them — over a link the very devices this
    repairs cannot currently make. Only the leaf carries the name, so only
    the leaf has to change.
    """
    from cryptography import x509
    from cryptography.hazmat.primitives import hashes, serialization
    from cryptography.hazmat.primitives.asymmetric import ec
    from cryptography.x509.oid import NameOID

    with open(os.path.join(tls_dir, "ca.pem"), "rb") as f:
        ca_cert = x509.load_pem_x509_certificate(f.read())
    with open(os.path.join(tls_dir, "ca.key"), "rb") as f:
        ca_key = serialization.load_pem_private_key(f.read(), password=None)

    now = datetime.datetime.now(datetime.timezone.utc)
    not_before = now - _NOT_BEFORE_BACKDATE
    not_after  = now + _VALIDITY

    srv_key = ec.generate_private_key(ec.SECP256R1())
    srv_cert = (
        x509.CertificateBuilder()
        .subject_name(x509.Name(
            [x509.NameAttribute(NameOID.COMMON_NAME, TLS_SERVER_NAME)]))
        .issuer_name(ca_cert.subject)
        .public_key(srv_key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(not_before)
        .not_valid_after(not_after)
        .add_extension(
            x509.SubjectAlternativeName(
                [x509.DNSName(n) for n in server_names()]),
            critical=False,
        )
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(
            x509.ExtendedKeyUsage([x509.oid.ExtendedKeyUsageOID.SERVER_AUTH]),
            critical=False,
        )
        .sign(ca_key, hashes.SHA256())
    )

    pem = serialization.Encoding.PEM

    def _write(path: str, data: bytes) -> None:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(data)

    _write(os.path.join(tls_dir, "server.key"), srv_key.private_bytes(
        pem, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    _write(os.path.join(tls_dir, "server.pem"), srv_cert.public_bytes(pem))
    log.warning(f"Re-issued the device-link server certificate from the "
                f"existing CA (SAN={', '.join(server_names())}). Devices keep "
                f"their installed ca.pem; no credential push is needed.")


def ensure_pki(db_path: str) -> str | None:
    """
    Make sure CA + server cert exist; returns the TLS dir, or None if the
    cryptography package is unavailable (TLS listener is then skipped —
    the plain listener keeps the fleet alive).

    A stored leaf that does not carry every name in `server_names()` is
    re-issued from the existing CA. That is the repair path for a
    controller carried across the rename: its leaf says
    `echomuse-controller`, firmware from v2.28.0-fx.1 demands
    `revoice-controller`, and the device has no way to report why it can no
    longer connect.
    """
    tls_dir = _tls_dir(db_path)
    needed  = ("ca.pem", "ca.key", "server.pem", "server.key")
    if all(os.path.exists(os.path.join(tls_dir, f)) for f in needed):
        absent = missing_names(_leaf_san_names(tls_dir), server_names())
        if absent:
            log.warning("The stored server certificate does not answer to "
                        + ", ".join(absent)
                        + " — firmware expecting one of those cannot verify "
                          "it and will retry forever. Re-issuing.")
            try:
                _reissue_leaf(tls_dir)
            except Exception as e:
                # A leaf that could not be re-issued is still the leaf that
                # was working yesterday for some of the fleet. Keep serving
                # it rather than taking the listener down.
                log.error(f"Could not re-issue the server certificate: {e}")
        return tls_dir
    try:
        import cryptography  # noqa: F401
    except ImportError:
        log.warning("python 'cryptography' package not installed — "
                    "device-link TLS listener disabled")
        return None
    _generate(tls_dir)
    return tls_dir


def server_ssl_context(tls_dir: str) -> ssl.SSLContext:
    """SSLContext for the device-facing wss listener (server-auth only)."""
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_2
    ctx.load_cert_chain(
        os.path.join(tls_dir, "server.pem"),
        os.path.join(tls_dir, "server.key"),
    )
    return ctx


def ca_pem(tls_dir: str) -> str:
    with open(os.path.join(tls_dir, "ca.pem"), "r", encoding="ascii") as f:
        return f.read()
