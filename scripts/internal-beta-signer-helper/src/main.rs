use base64::{
    engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD},
    Engine,
};
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
const OPERATION_BINDING_FILE: &str = "beta.5-sequence-5.payload-sha256";
const MAX_KEY_BYTES: u64 = 16 * 1024;
const MAX_PUBLIC_BYTES: u64 = 4 * 1024;
const MAX_PAYLOAD_BYTES: u64 = 64 * 1024;
const MAX_INSTALLER_BYTES: u64 = 2 * 1024 * 1024 * 1024;
const MAX_SAFE_INTEGER: u64 = 9_007_199_254_740_991;
const MAX_MANIFEST_LIFETIME_MS: i64 = 7 * 24 * 60 * 60 * 1000;
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

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PayloadRecord {
    schema: String,
    channel: String,
    #[serde(rename = "keyId")]
    key_id: String,
    version: String,
    platform: String,
    architecture: String,
    sequence: u64,
    #[serde(rename = "installerUrl")]
    installer_url: String,
    #[serde(rename = "immutableUrl")]
    immutable_url: String,
    size: u64,
    sha256: String,
    sha512: String,
    #[serde(rename = "publishedAt")]
    published_at: String,
    #[serde(rename = "notBefore")]
    not_before: String,
    #[serde(rename = "expiresAt")]
    expires_at: String,
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

fn decimal(bytes: &[u8]) -> Result<i64> {
    if bytes.is_empty() || !bytes.iter().all(u8::is_ascii_digit) {
        return reject(Failure::Payload);
    }
    bytes.iter().try_fold(0_i64, |value, byte| {
        value
            .checked_mul(10)
            .and_then(|value| value.checked_add(i64::from(byte - b'0')))
            .ok_or(Failure::Payload)
    })
}

fn leap_year(year: i64) -> bool {
    year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)
}

fn canonical_time_ms(value: &str) -> Result<i64> {
    let bytes = value.as_bytes();
    if bytes.len() != 24
        || bytes[4] != b'-'
        || bytes[7] != b'-'
        || bytes[10] != b'T'
        || bytes[13] != b':'
        || bytes[16] != b':'
        || bytes[19] != b'.'
        || bytes[23] != b'Z'
    {
        return reject(Failure::Payload);
    }
    let year = decimal(&bytes[0..4])?;
    let month = decimal(&bytes[5..7])?;
    let day = decimal(&bytes[8..10])?;
    let hour = decimal(&bytes[11..13])?;
    let minute = decimal(&bytes[14..16])?;
    let second = decimal(&bytes[17..19])?;
    let millisecond = decimal(&bytes[20..23])?;
    if year < 1 || !(1..=12).contains(&month) || hour > 23 || minute > 59 || second > 59 {
        return reject(Failure::Payload);
    }
    let month_days = [
        31,
        if leap_year(year) { 29 } else { 28 },
        31,
        30,
        31,
        30,
        31,
        31,
        30,
        31,
        30,
        31,
    ];
    if day < 1 || day > month_days[(month - 1) as usize] {
        return reject(Failure::Payload);
    }
    let prior_year = year - 1;
    let days_before_year = 365 * prior_year + prior_year / 4 - prior_year / 100 + prior_year / 400;
    let epoch_prior_year = 1969;
    let days_before_epoch = 365 * epoch_prior_year + epoch_prior_year / 4 - epoch_prior_year / 100
        + epoch_prior_year / 400;
    let days_before_month: i64 = month_days[..(month - 1) as usize].iter().sum();
    let days = days_before_year - days_before_epoch + days_before_month + day - 1;
    (((days * 24 + hour) * 60 + minute) * 60 + second)
        .checked_mul(1000)
        .and_then(|value| value.checked_add(millisecond))
        .ok_or(Failure::Payload)
}

fn validate_payload(payload: &[u8]) -> Result<()> {
    let record: PayloadRecord = serde_json::from_slice(payload).map_err(|_| Failure::Payload)?;
    let filename = "Void-Code-0.1.3-beta.5-windows-x64.exe";
    let sha512 = STANDARD
        .decode(&record.sha512)
        .map_err(|_| Failure::Payload)?;
    let published = canonical_time_ms(&record.published_at)?;
    let not_before = canonical_time_ms(&record.not_before)?;
    let expires = canonical_time_ms(&record.expires_at)?;
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| Failure::Internal)?;
    let now_ms = i64::try_from(now.as_millis()).map_err(|_| Failure::Internal)?;
    if record.schema != "vc-windows-update-v1"
        || record.channel != "closed-beta"
        || record.key_id != PINNED_KEY_ID
        || record.version != "0.1.3-beta.5"
        || record.platform != "win32"
        || record.architecture != "x64"
        || record.sequence != 5
        || record.sequence > MAX_SAFE_INTEGER
        || record.size < 1
        || record.size > MAX_INSTALLER_BYTES
        || record.sha256.len() != 64
        || !record.sha256.bytes().all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        || sha512.len() != 64
        || STANDARD.encode(&sha512) != record.sha512
        || record.installer_url != format!("https://vc.makscee.ru/download/windows/{filename}")
        || record.immutable_url != format!("https://github.com/makscee/void-code/releases/download/desktop-v0.1.3-beta.5/{filename}")
        || published > not_before
        || not_before > expires
        || expires - published > MAX_MANIFEST_LIFETIME_MS
        || expires <= now_ms
    {
        return reject(Failure::Payload);
    }
    Ok(())
}

fn bind_operation(paths: &Paths, payload: &[u8]) -> Result<()> {
    let binding = paths.used.join(OPERATION_BINDING_FILE);
    let expected = format!("{:x}\n", Sha256::digest(payload));
    if atomic_new_output(&binding, expected.as_bytes()).is_ok() {
        return Ok(());
    }
    let existing = read_checked(&binding, 65, Failure::Payload)?;
    if existing != expected.as_bytes() {
        return reject(Failure::Payload);
    }
    Ok(())
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
    validate_payload(&payload)?;
    output_absent(&paths.output)?;
    bind_operation(paths, &payload)?;
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
        fs::write(&payload, fixture_payload(1)).expect("write payload");
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

    fn fixture_payload(size: u64) -> Vec<u8> {
        format!(
            "{{\"schema\":\"vc-windows-update-v1\",\"channel\":\"closed-beta\",\"keyId\":\"{PINNED_KEY_ID}\",\"version\":\"0.1.3-beta.5\",\"platform\":\"win32\",\"architecture\":\"x64\",\"sequence\":5,\"installerUrl\":\"https://vc.makscee.ru/download/windows/Void-Code-0.1.3-beta.5-windows-x64.exe\",\"immutableUrl\":\"https://github.com/makscee/void-code/releases/download/desktop-v0.1.3-beta.5/Void-Code-0.1.3-beta.5-windows-x64.exe\",\"size\":{size},\"sha256\":\"{}\",\"sha512\":\"{}\",\"publishedAt\":\"2099-01-01T00:00:00.000Z\",\"notBefore\":\"2099-01-01T00:00:00.000Z\",\"expiresAt\":\"2099-01-08T00:00:00.000Z\"}}",
            "a".repeat(64),
            STANDARD.encode([1_u8; 64]),
        ).into_bytes()
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
        validate_payload(&payload)?;
        output_absent(&paths.output)?;
        bind_operation(paths, &payload)?;
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
    fn validates_fixed_identity_and_numeric_boundaries() {
        assert!(validate_payload(&fixture_payload(1)).is_ok());
        assert!(validate_payload(&fixture_payload(MAX_INSTALLER_BYTES)).is_ok());
        assert!(validate_payload(&fixture_payload(MAX_INSTALLER_BYTES + 1)).is_err());
        for malformed in [
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace("\"size\":1", "\"size\":true"),
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace("\"sequence\":5", "\"sequence\":9007199254740992"),
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace("0.1.3-beta.5", "0.1.3-beta.6"),
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace("2099-01-01T00:00:00.000Z", "2099-02-30T00:00:00.000Z"),
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace("2099-01-08T00:00:00.000Z", "2020-01-08T00:00:00.000Z"),
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace(
                    "\"publishedAt\":\"2099-01-01T00:00:00.000Z\"",
                    "\"publishedAt\":\"2099-01-01T00:00:00Z\"",
                ),
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace(
                    "\"publishedAt\":\"2099-01-01T00:00:00.000Z\"",
                    "\"publishedAt\":\"2099-01-02T00:00:00.000Z\"",
                ),
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace("2099-01-08T00:00:00.000Z", "2099-01-08T00:00:00.001Z"),
            String::from_utf8(fixture_payload(1))
                .expect("utf8")
                .replace("2099-", "2020-"),
        ] {
            assert!(validate_payload(malformed.as_bytes()).is_err());
        }
    }

    #[test]
    fn binds_one_operation_across_restart_rejects_equivocation_and_cleans_outputs() {
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
        let first_signature = fs::read(&fixture.output).expect("first signature");
        fs::remove_file(&fixture.output).expect("consume signed output");
        run_fixture(&fixture).expect("identical payload remains deterministic after restart");
        assert_eq!(
            fs::read(&fixture.output).expect("second signature"),
            first_signature
        );
        fs::remove_file(&fixture.output).expect("consume repeated output");
        fs::write(&fixture.payload, fixture_payload(2)).expect("distinct same-operation payload");
        fs::set_permissions(&fixture.payload, fs::Permissions::from_mode(0o600))
            .expect("payload mode");
        assert!(
            run_fixture(&fixture).is_err(),
            "same-identity equivocation was accepted"
        );
        assert!(!fixture.output.exists(), "equivocation left output");
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
