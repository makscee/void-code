use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use ed25519_dalek::{pkcs8::DecodePrivateKey, Signer, SigningKey};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::{
    env,
    ffi::CString,
    ffi::OsString,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    os::unix::fs::{MetadataExt, OpenOptionsExt},
    path::{Path, PathBuf},
    process,
    time::{SystemTime, UNIX_EPOCH},
};

const KEY: &str = "/var/lib/vc-internal-beta-signer/ed25519-private.pem";
const PUBLIC: &str = "/var/lib/vc-internal-beta-signer/public.json";
const PAYLOAD: &str = "/run/vc-internal-beta-signer/payload";
const OUT: &str = "/run/vc-internal-beta-signer/signature";
const USED: &str = "/var/lib/vc-internal-beta-signer/used";
const MAX_KEY_BYTES: u64 = 16 * 1024;
const MAX_PUBLIC_BYTES: u64 = 4 * 1024;
const MAX_PAYLOAD_BYTES: u64 = 64 * 1024;
const PINNED_KEY_ID: &str = "internal-beta-2026-08";
const PINNED_PUBLIC_KEY: &str = "rLWIrvTJV3Sv1pDk-FaYGCNadFEU_7pPD7sBvb_bfAc";

#[derive(Clone, Copy, Debug)]
enum Failure {
    Args,
    Privilege,
    Key,
    Public,
    Payload,
    Output,
    Internal,
}

impl Failure {
    const fn label(self) -> &'static str {
        match self {
            Self::Args => "arguments",
            Self::Privilege => "privilege",
            Self::Key => "key",
            Self::Public => "public",
            Self::Payload => "payload",
            Self::Output => "output",
            Self::Internal => "internal",
        }
    }
}

type Result<T> = std::result::Result<T, Failure>;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PublicRecord {
    schema: String,
    #[serde(rename = "keyId")]
    key_id: String,
    purpose: String,
    #[serde(rename = "publicKeyBase64url")]
    public_key: String,
    #[serde(rename = "fingerprintSha256")]
    fingerprint: String,
}

struct Paths {
    key: PathBuf,
    public: PathBuf,
    payload: PathBuf,
    output: PathBuf,
    used: PathBuf,
}

impl Paths {
    fn fixed() -> Self {
        Self {
            key: PathBuf::from(KEY),
            public: PathBuf::from(PUBLIC),
            payload: PathBuf::from(PAYLOAD),
            output: PathBuf::from(OUT),
            used: PathBuf::from(USED),
        }
    }
}

fn reject<T>(failure: Failure) -> Result<T> {
    Err(failure)
}

fn checked_file(path: &Path, maximum: u64, failure: Failure) -> Result<File> {
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(path)
        .map_err(|_| failure)?;
    let metadata = file.metadata().map_err(|_| failure)?;
    if !metadata.is_file()
        || metadata.uid() != 0
        || (metadata.mode() & 0o7777) != 0o600
        || metadata.len() > maximum
    {
        return reject(failure);
    }
    Ok(file)
}

fn read_checked(path: &Path, maximum: u64, failure: Failure) -> Result<Vec<u8>> {
    let file = checked_file(path, maximum, failure)?;
    let capacity = usize::try_from(maximum).map_err(|_| failure)?;
    let mut bytes = Vec::with_capacity(capacity);
    file.take(maximum + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| failure)?;
    if u64::try_from(bytes.len()).map_err(|_| failure)? > maximum {
        return reject(failure);
    }
    Ok(bytes)
}

fn decode_canonical_public(value: &str) -> Result<[u8; 32]> {
    let decoded = URL_SAFE_NO_PAD.decode(value).map_err(|_| Failure::Public)?;
    let raw: [u8; 32] = decoded.try_into().map_err(|_| Failure::Public)?;
    if URL_SAFE_NO_PAD.encode(raw) != value {
        return reject(Failure::Public);
    }
    Ok(raw)
}

fn validate_public_record(bytes: &[u8], derived: &[u8; 32], pinned: &str) -> Result<()> {
    let record: PublicRecord = serde_json::from_slice(bytes).map_err(|_| Failure::Public)?;
    if record.schema != "vc-internal-beta-public-v1"
        || record.key_id != PINNED_KEY_ID
        || record.purpose != "INTERNAL-BETA-ONLY"
    {
        return reject(Failure::Public);
    }
    let record_public = decode_canonical_public(&record.public_key)?;
    if record.fingerprint != format!("{:x}", Sha256::digest(record_public)) {
        return reject(Failure::Public);
    }
    let pinned_public = decode_canonical_public(pinned)?;
    if record_public != *derived || pinned_public != *derived {
        return reject(Failure::Public);
    }
    Ok(())
}

fn checked_output_parent(path: &Path) -> Result<(&Path, File)> {
    let parent = path.parent().ok_or(Failure::Output)?;
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC)
        .open(parent)
        .map_err(|_| Failure::Output)?;
    let metadata = file.metadata().map_err(|_| Failure::Output)?;
    if !metadata.is_dir() || metadata.uid() != 0 || (metadata.mode() & 0o022) != 0 {
        return reject(Failure::Output);
    }
    Ok((parent, file))
}

fn output_absent(path: &Path) -> Result<()> {
    match fs::symlink_metadata(path) {
        Ok(_) => reject(Failure::Output),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(_) => reject(Failure::Output),
    }
}

fn atomic_new_output(path: &Path, bytes: &[u8]) -> Result<()> {
    atomic_new_output_inner(path, bytes, false)
}

fn atomic_new_output_inner(
    path: &Path,
    bytes: &[u8],
    force_failure_before_rename: bool,
) -> Result<()> {
    let (parent, directory) = checked_output_parent(path)?;
    output_absent(path)?;
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| Failure::Internal)?
        .as_nanos();
    let temporary = parent.join(format!(".signature-{}-{nonce}", process::id()));
    let result = (|| {
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&temporary)
            .map_err(|_| Failure::Output)?;
        file.write_all(bytes).map_err(|_| Failure::Output)?;
        file.sync_all().map_err(|_| Failure::Output)?;
        drop(file);
        if force_failure_before_rename {
            return reject(Failure::Output);
        }
        let temporary_c = CString::new(temporary.as_os_str().as_encoded_bytes())
            .map_err(|_| Failure::Internal)?;
        let output_c =
            CString::new(path.as_os_str().as_encoded_bytes()).map_err(|_| Failure::Internal)?;
        // Linux renameat2(RENAME_NOREPLACE) closes the check/rename race without ever replacing an output.
        let renamed = unsafe {
            libc::syscall(
                libc::SYS_renameat2,
                libc::AT_FDCWD,
                temporary_c.as_ptr(),
                libc::AT_FDCWD,
                output_c.as_ptr(),
                libc::RENAME_NOREPLACE,
            )
        };
        if renamed != 0 {
            return reject(Failure::Output);
        }
        directory.sync_all().map_err(|_| Failure::Output)
    })();
    if result.is_err() {
        let _ = fs::remove_file(&temporary);
    }
    result
}

fn no_arguments(arguments: impl Iterator<Item = OsString>) -> bool {
    arguments.count() == 1
}

fn mark_payload_used(paths: &Paths, payload: &[u8]) -> Result<()> {
    let digest = format!("{:x}", Sha256::digest(payload));
    atomic_new_output(&paths.used.join(digest), b"signed\n")
}

fn run(paths: &Paths) -> Result<()> {
    if !no_arguments(env::args_os()) {
        return reject(Failure::Args);
    }
    if unsafe { libc::geteuid() } != 0 {
        return reject(Failure::Privilege);
    }
    let key_bytes = read_checked(&paths.key, MAX_KEY_BYTES, Failure::Key)?;
    let key_pem = std::str::from_utf8(&key_bytes).map_err(|_| Failure::Key)?;
    let signing_key = SigningKey::from_pkcs8_pem(key_pem).map_err(|_| Failure::Key)?;
    let derived = signing_key.verifying_key().to_bytes();
    let public = read_checked(&paths.public, MAX_PUBLIC_BYTES, Failure::Public)?;
    validate_public_record(&public, &derived, PINNED_PUBLIC_KEY)?;
    let payload = read_checked(&paths.payload, MAX_PAYLOAD_BYTES, Failure::Payload)?;
    if payload.is_empty() {
        return reject(Failure::Payload);
    }
    output_absent(&paths.output)?;
    mark_payload_used(paths, &payload)?;
    let signature = signing_key.sign(&payload).to_bytes();
    atomic_new_output(&paths.output, &signature)
}

fn main() {
    if let Err(failure) = run(&Paths::fixed()) {
        eprintln!("rejected:{}", failure.label());
        process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ed25519_dalek::pkcs8::EncodePrivateKey;
    use std::os::unix::fs::{symlink, PermissionsExt};
    use tempfile::TempDir;

    fn paths(temp: &TempDir) -> Paths {
        let private = temp.path().join("ed25519-private.pem");
        let public = temp.path().join("public.json");
        let payload = temp.path().join("payload");
        let output = temp.path().join("signature");
        let key = SigningKey::from_bytes(&[7; 32]);
        let der = key.to_pkcs8_der().expect("test key");
        let encoded = base64::engine::general_purpose::STANDARD.encode(der.as_bytes());
        let body = encoded
            .as_bytes()
            .chunks(64)
            .map(std::str::from_utf8)
            .collect::<std::result::Result<Vec<_>, _>>()
            .expect("valid base64")
            .join("\n");
        let pem = format!("-----BEGIN PRIVATE KEY-----\n{body}\n-----END PRIVATE KEY-----\n");
        fs::write(&private, pem).expect("write key");
        fs::set_permissions(&private, fs::Permissions::from_mode(0o600)).expect("mode key");
        let raw = key.verifying_key().to_bytes();
        let record = format!(
            "{{\"schema\":\"vc-internal-beta-public-v1\",\"keyId\":\"{PINNED_KEY_ID}\",\"purpose\":\"INTERNAL-BETA-ONLY\",\"publicKeyBase64url\":\"{}\",\"fingerprintSha256\":\"{}\"}}",
            URL_SAFE_NO_PAD.encode(raw),
            format!("{:x}", Sha256::digest(raw)),
        );
        fs::write(&public, record).expect("write public");
        fs::set_permissions(&public, fs::Permissions::from_mode(0o600)).expect("mode public");
        fs::write(&payload, b"exact payload").expect("write payload");
        fs::set_permissions(&payload, fs::Permissions::from_mode(0o600)).expect("mode payload");
        let used = temp.path().join("used");
        fs::create_dir(&used).expect("used directory");
        fs::set_permissions(&used, fs::Permissions::from_mode(0o700)).expect("mode used directory");
        Paths {
            key: private,
            public,
            payload,
            output,
            used,
        }
    }

    // The real pinned key cannot be generated by a test fixture. These tests exercise the
    // same parser and filesystem boundary after temporarily matching the fixture-derived key.
    fn run_fixture(paths: &Paths) -> Result<()> {
        let key_bytes = read_checked(&paths.key, MAX_KEY_BYTES, Failure::Key)?;
        let signing_key =
            SigningKey::from_pkcs8_pem(std::str::from_utf8(&key_bytes).map_err(|_| Failure::Key)?)
                .map_err(|_| Failure::Key)?;
        let derived = signing_key.verifying_key().to_bytes();
        let public = read_checked(&paths.public, MAX_PUBLIC_BYTES, Failure::Public)?;
        let record: PublicRecord = serde_json::from_slice(&public).map_err(|_| Failure::Public)?;
        validate_public_record(&public, &derived, &record.public_key)?;
        let payload = read_checked(&paths.payload, MAX_PAYLOAD_BYTES, Failure::Payload)?;
        output_absent(&paths.output)?;
        mark_payload_used(paths, &payload)?;
        atomic_new_output(&paths.output, &signing_key.sign(&payload).to_bytes())
    }

    #[test]
    fn rejects_arguments() {
        assert!(no_arguments(vec![OsString::from("helper")].into_iter()));
        assert!(!no_arguments(
            vec![OsString::from("helper"), OsString::from("forbidden")].into_iter()
        ));
    }

    #[test]
    fn signs_exact_payload_to_a_new_root_only_file() {
        if unsafe { libc::geteuid() } != 0 {
            return;
        }
        let temp = TempDir::new().expect("temp");
        fs::set_permissions(temp.path(), fs::Permissions::from_mode(0o700)).expect("mode dir");
        let fixture = paths(&temp);
        let fixture_key = SigningKey::from_bytes(&[7; 32]);
        let public =
            read_checked(&fixture.public, MAX_PUBLIC_BYTES, Failure::Public).expect("public");
        assert!(validate_public_record(
            &public,
            &fixture_key.verifying_key().to_bytes(),
            PINNED_PUBLIC_KEY
        )
        .is_err());
        run_fixture(&fixture).expect("success");
        let signature = fs::read(&fixture.output).expect("signature");
        assert_eq!(signature.len(), 64);
        assert_eq!(
            fs::metadata(&fixture.output).expect("metadata").mode() & 0o777,
            0o600
        );
    }

    #[test]
    fn rejects_malformed_key_symlink_bad_mode_oversize_and_public_mismatch() {
        if unsafe { libc::geteuid() } != 0 {
            return;
        }
        for mutation in ["malformed", "symlink", "mode", "oversize", "public"] {
            let temp = TempDir::new().expect("temp");
            fs::set_permissions(temp.path(), fs::Permissions::from_mode(0o700)).expect("mode dir");
            let fixture = paths(&temp);
            match mutation {
                "malformed" => fs::write(&fixture.key, b"not a key").expect("bad key"),
                "symlink" => {
                    fs::remove_file(&fixture.payload).expect("remove payload");
                    symlink("/etc/passwd", &fixture.payload).expect("symlink");
                }
                "mode" => fs::set_permissions(&fixture.key, fs::Permissions::from_mode(0o640))
                    .expect("bad mode"),
                "oversize" => {
                    fs::write(&fixture.payload, vec![0; (MAX_PAYLOAD_BYTES + 1) as usize])
                        .expect("large payload");
                    fs::set_permissions(&fixture.payload, fs::Permissions::from_mode(0o600))
                        .expect("mode payload");
                }
                "public" => fs::write(&fixture.public, "{}").expect("bad public"),
                _ => unreachable!(),
            }
            assert!(run_fixture(&fixture).is_err(), "{mutation} was accepted");
            assert!(!fixture.output.exists(), "{mutation} left output");
        }
    }

    #[test]
    fn rejects_replay_existing_or_symlink_output_and_cleans_failed_temporary_output() {
        if unsafe { libc::geteuid() } != 0 {
            return;
        }
        let temp = TempDir::new().expect("temp");
        fs::set_permissions(temp.path(), fs::Permissions::from_mode(0o700)).expect("mode dir");
        let fixture = paths(&temp);
        fs::write(&fixture.output, b"existing").expect("existing output");
        assert!(run_fixture(&fixture).is_err());
        fs::remove_file(&fixture.output).expect("remove existing output");
        run_fixture(&fixture).expect("first sign");
        fs::remove_file(&fixture.output).expect("remove signed output");
        assert!(run_fixture(&fixture).is_err(), "replay was accepted");
        let symlink_temp = TempDir::new().expect("symlink temp");
        fs::set_permissions(symlink_temp.path(), fs::Permissions::from_mode(0o700))
            .expect("mode dir");
        let symlink_fixture = paths(&symlink_temp);
        symlink("/etc/passwd", &symlink_fixture.output).expect("output symlink");
        assert!(run_fixture(&symlink_fixture).is_err());
        assert!(fs::symlink_metadata(&symlink_fixture.output)
            .expect("symlink retained")
            .file_type()
            .is_symlink());
        let cleanup_temp = TempDir::new().expect("cleanup temp");
        fs::set_permissions(cleanup_temp.path(), fs::Permissions::from_mode(0o700))
            .expect("mode dir");
        let cleanup_output = cleanup_temp.path().join("signature");
        assert!(atomic_new_output_inner(&cleanup_output, b"test", true).is_err());
        assert_eq!(
            fs::read_dir(cleanup_temp.path())
                .expect("cleanup dir")
                .filter_map(|item| item.ok())
                .filter(|item| item
                    .file_name()
                    .to_string_lossy()
                    .starts_with(".signature-"))
                .count(),
            0
        );
        assert_eq!(
            fs::read_dir(symlink_temp.path())
                .expect("dir")
                .filter_map(|item| item.ok())
                .filter(|item| item
                    .file_name()
                    .to_string_lossy()
                    .starts_with(".signature-"))
                .count(),
            0
        );
    }
}
