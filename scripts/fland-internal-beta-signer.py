#!/usr/bin/env python3
"""Root-only fixed-operation signer for Void Code INTERNAL-BETA; never a generic signer."""
import base64, hashlib, json, os, re, stat, subprocess, sys, tempfile
from datetime import datetime, timezone

ROOT = '/var/lib/vc-internal-beta-signer'
KEY = ROOT + '/ed25519-private.pem'
PUBLIC = ROOT + '/public.json'
REQUEST = '/run/vc-internal-beta-signer/request.json'
RESULT = '/run/vc-internal-beta-signer/result.json'
PAYLOAD = '/run/vc-internal-beta-signer/payload'
SIGNATURE = '/run/vc-internal-beta-signer/signature'
HELPER = '/usr/local/libexec/vc-internal-beta-signer-helper'
OPERATION_BINDING = ROOT + '/used/beta.5-sequence-5.payload-sha256'
KEY_ID = 'internal-beta-2026-08'
MAX_SAFE_INTEGER = 9_007_199_254_740_991
MAX_INSTALLER_BYTES = 2 * 1024 * 1024 * 1024
PREDECESSOR_SHA256 = '6e2073dd8b6dae2f07adf915d6ea895f2e33e6362851c6777de6067a456d08fd'
SHA256 = re.compile(r'^[0-9a-f]{64}$')
B64URL = re.compile(r'^[A-Za-z0-9_-]{2,}$')
B64_512 = re.compile(r'^(?:[A-Za-z0-9+/]{4}){21}[A-Za-z0-9+/]{2}==$')


def die(message): raise ValueError(message)
def exact(obj, keys):
    if not isinstance(obj, dict) or set(obj) != set(keys): die('schema rejected')
def strict_json(data):
    def duplicate(pairs):
        out = {}
        for key, value in pairs:
            if key in out: die('duplicate member')
            out[key] = value
        return out
    return json.loads(data.decode('utf-8'), object_pairs_hook=duplicate)
def b64decode(value, length=None):
    if not isinstance(value, str) or not B64URL.fullmatch(value) or '=' in value: die('encoding rejected')
    raw = base64.urlsafe_b64decode(value + '=' * (-len(value) % 4))
    if base64.urlsafe_b64encode(raw).rstrip(b'=').decode() != value or (length is not None and len(raw) != length): die('encoding rejected')
    return raw
def canonical_time(value):
    if not isinstance(value, str) or not re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z', value): die('time rejected')
    parsed = datetime.fromisoformat(value[:-1] + '+00:00')
    if parsed.isoformat(timespec='milliseconds').replace('+00:00', 'Z') != value: die('time rejected')
    return parsed
def secure_file(path):
    info = os.stat(path, follow_symlinks=False)
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077: die('root-owned input required')
def write_all(fd, data):
    view = memoryview(data)
    while view:
        written = os.write(fd, view)
        if written < 1: die('internal-validation')
        view = view[written:]

def atomic(path, data, mode=0o600):
    directory = os.path.dirname(path); fd, temporary = tempfile.mkstemp(prefix='.tmp-', dir=directory)
    try:
        os.fchmod(fd, mode); write_all(fd, data); os.fsync(fd); os.close(fd); os.replace(temporary, path)
        os.fsync(os.open(directory, os.O_RDONLY))
    finally:
        try: os.unlink(temporary)
        except FileNotFoundError: pass

def atomic_new(path, data, mode=0o600):
    directory = os.path.dirname(path); fd, temporary = tempfile.mkstemp(prefix='.tmp-', dir=directory)
    try:
        os.fchmod(fd, mode); write_all(fd, data); os.fsync(fd); os.close(fd)
        os.link(temporary, path, follow_symlinks=False); os.fsync(os.open(directory, os.O_RDONLY))
    finally:
        try: os.unlink(temporary)
        except FileNotFoundError: pass

def validate_payload(value):
    exact(value, ['architecture','channel','expiresAt','immutableUrl','installerUrl','keyId','notBefore','platform','publishedAt','schema','sequence','sha256','sha512','size','version'])
    if value['schema'] != 'vc-windows-update-v1' or value['channel'] != 'closed-beta' or value['keyId'] != KEY_ID or value['version'] != '0.1.3-beta.5' or value['platform'] != 'win32' or value['architecture'] != 'x64': die('payload identity rejected')
    if type(value['sequence']) is not int or value['sequence'] != 5 or value['sequence'] > MAX_SAFE_INTEGER or type(value['size']) is not int or value['size'] < 1 or value['size'] > MAX_INSTALLER_BYTES or not isinstance(value['sha256'], str) or not SHA256.fullmatch(value['sha256']) or not isinstance(value['sha512'], str) or not B64_512.fullmatch(value['sha512']): die('payload artifact rejected')
    version = value['version']; filename = f'Void-Code-{version}-windows-x64.exe'
    if value['installerUrl'] != f'https://vc.makscee.ru/download/windows/{filename}' or value['immutableUrl'] != f'https://github.com/makscee/void-code/releases/download/desktop-v{version}/{filename}': die('payload URLs rejected')
    published, notbefore, expiry = (canonical_time(value[k]) for k in ('publishedAt','notBefore','expiresAt'))
    if published > notbefore or notbefore > expiry or (expiry - published).total_seconds() > 604800: die('payload time rejected')
    if expiry <= datetime.now(timezone.utc): die('payload expired')


def bind_operation(path, digest):
    encoded = (digest + '\n').encode()
    try:
        atomic_new(path, encoded)
        return
    except FileExistsError:
        pass
    secure_file(path)
    with open(path, 'rb') as binding:
        if binding.read(66) != encoded: die('operation payload mismatch')


def initialize():
    die('initialization disabled: provision the enrolled key only through the attended external ceremony')

def sign():
    if os.geteuid() != 0: die('root required')
    os.umask(0o077); secure_file(KEY); secure_file(PUBLIC); secure_file(REQUEST)
    request = strict_json(open(REQUEST, 'rb').read())
    exact(request, ['app','channel','fromArtifactSha256','fromVersion','keyId','payload','schema'])
    if request['schema'] != 'vc-internal-beta-sign-request-v1' or request['app'] != 'Void Code' or request['channel'] != 'closed-beta' or request['fromVersion'] != '0.1.3-beta.4' or request['keyId'] != KEY_ID or request['fromArtifactSha256'] != PREDECESSOR_SHA256: die('request rejected')
    payload = b64decode(request['payload']); value = strict_json(payload)
    validate_payload(value)
    digest = hashlib.sha256(payload).hexdigest()
    bind_operation(OPERATION_BINDING, digest)
    if os.path.lexists(PAYLOAD) or os.path.lexists(SIGNATURE) or os.path.lexists(RESULT): die('payload already signed')
    try:
        atomic_new(PAYLOAD, payload)
        subprocess.run([HELPER], check=True, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        secure_file(SIGNATURE); signature = open(SIGNATURE, 'rb').read(65)
        if len(signature) != 64: die('signature rejected')
        atomic_new(RESULT, (json.dumps({'schema':'vc-internal-beta-sign-result-v1','keyId':KEY_ID,'payloadSha256':digest,'signatureBase64url':base64.urlsafe_b64encode(signature).rstrip(b'=').decode(),'status':'signed'}, separators=(',',':'))+'\n').encode())
    finally:
        for path in (PAYLOAD, SIGNATURE):
            try: os.unlink(path)
            except FileNotFoundError: pass

def revoke():
    if os.geteuid() != 0: die('root required')
    atomic(ROOT + '/REVOKED', b'INTERNAL-BETA signing disabled; stable/public use was always forbidden.\n')

def main():
    if len(sys.argv) != 2 or sys.argv[1] not in ('initialize','sign','revoke'): die('fixed operation required')
    if sys.argv[1] == 'initialize': initialize()
    elif sys.argv[1] == 'revoke': revoke()
    else:
        if os.path.exists(ROOT + '/REVOKED'): die('signer revoked')
        sign()
if __name__ == '__main__':
    try: main()
    except ValueError as error:
        # Fixed, value-free rejection labels only; never echo request/key/path data.
        known = {'schema rejected', 'duplicate member', 'encoding rejected', 'time rejected', 'root-owned input required', 'request rejected', 'payload identity rejected', 'payload artifact rejected', 'payload URLs rejected', 'payload time rejected', 'payload expired', 'operation payload mismatch', 'payload already signed', 'signer revoked', 'root required'}
        sys.stderr.write(f"rejected:{error if str(error) in known else 'internal-validation'}\n")
        sys.exit(1)
    except subprocess.CalledProcessError:
        sys.stderr.write('rejected:crypto-operation\n')
        sys.exit(1)
    except Exception:
        sys.stderr.write('rejected:internal\n')
        sys.exit(1)
