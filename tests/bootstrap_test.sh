#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/../setup.sh"
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
printf 'test bundle\n' > "$test_dir/bundle.tar.gz"
(cd "$test_dir" && sha256sum bundle.tar.gz > bundle.tar.gz.sha256)
valid_checksum "$test_dir/bundle.tar.gz.sha256" "$test_dir/bundle.tar.gz" bundle.tar.gz
printf 'PASS valid checksum\n'
printf 'damage\n' >> "$test_dir/bundle.tar.gz"
if valid_checksum "$test_dir/bundle.tar.gz.sha256" "$test_dir/bundle.tar.gz" bundle.tar.gz; then exit 1; fi
printf 'PASS damaged archive rejected\n'
sed 's/bundle.tar.gz/other.tar.gz/' "$test_dir/bundle.tar.gz.sha256" > "$test_dir/wrong-name"
if valid_checksum "$test_dir/wrong-name" "$test_dir/bundle.tar.gz" bundle.tar.gz; then exit 1; fi
printf 'PASS checksum filename mismatch rejected\n'
valid_member release/install.sh release
for member in /etc/passwd other/install.sh release/../../etc/passwd release/.. 'release/a\b'; do
    if valid_member "$member" release; then exit 1; fi
done
printf 'PASS archive traversal and wrong root rejected\n'
if (main --unknown); then exit 1; fi
printf 'PASS invalid invocation rejected\n'
fixture_dir="$test_dir/download"
archive_root="soar-server-setup-$SETUP_RELEASE-linux-amd64"
mkdir -p "$fixture_dir/$archive_root"
printf '#!/usr/bin/env bash\nexit 0\n' > "$fixture_dir/$archive_root/install.sh"
tar -czf "$fixture_dir/$archive_root.tar.gz" -C "$fixture_dir" "$archive_root/install.sh"
(cd "$fixture_dir" && sha256sum "$archive_root.tar.gz" > "$archive_root.tar.gz.sha256")
curl() {
    local arg url= output=${@: -1}
    for arg in "$@"; do [[ $arg != https://* ]] || url=$arg; done
    [[ $url == "https://github.com/WhiteDNS/Soar-Server/releases/download/v$SETUP_RELEASE/"* ]] || return 1
    cp -- "$fixture_dir/${url##*/}" "$output"
}
(
    # /etc/os-release sets this exact variable on Ubuntu. Download version
    # must remain the installer release, including on the normal install path.
    VERSION='24.04.4 LTS (Noble Numbat)'
    main --check-download
)
printf 'PASS Ubuntu VERSION cannot replace the release used for downloads\n'
printf '6 bootstrap checks passed.\n'
