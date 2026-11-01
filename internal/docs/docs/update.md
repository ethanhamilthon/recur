# Updates

## recur update

`recur update` installs the latest GitHub release over the running executable. It is written in Go and
needs neither curl nor sh:
1. It downloads `checksums.txt` and `recur_<os>_<arch>.tar.gz` from that release.
2. It verifies the archive's SHA-256 against its line in `checksums.txt`.
3. It extracts the `recur` binary from the archive into a temporary file beside the installed executable.
4. It renames that file over the installed executable, so the replacement is atomic. Symlinks are resolved first.

Only darwin and linux on amd64 and arm64 are supported. If any step fails, the installed binary is left
untouched. When the installed version is already the latest, it says so and changes nothing. After an
update, restart recur.

`scripts/install.sh` (see the README) is only for the first install. It honors `RECUR_BINDIR` (install
directory) and `RECUR_VERSION` (pinned release tag). `recur update` does not read either variable.

`recur update --check` only prints `installed vX, latest vY`. When a newer
release exists, it says `run recur update to install it`. Nothing is installed.

## Startup Update Check

The TUI checks the latest GitHub release in the background at startup. When one exists, the welcome line
shows `vX available — run recur update`. The result is cached in `$RECUR_HOME/update.json` for 6 hours.
The file has two fields: `checked` (the time of the check) and `latest` (the release tag, such as `vX.Y.Z`).
A failed check is silent and is not cached. Development builds with a non-version string skip the check.

Set `RECUR_NO_UPDATE_CHECK` to any nonempty value to turn the startup check off. The variable is listed
in `recur docs cli`. `recur update --check` still works.
