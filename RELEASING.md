# Releasing a new version

This workflow is **not** a plain Alfred export. The `.alfredworkflow` bundle must
contain:

1. A **universal Go binary** (`michelin`, amd64 + arm64) that is **Developer ID
   signed and notarized** — otherwise macOS Gatekeeper blocks it for anyone who
   downloads the release.
2. A **zipped database** (`michelin.db.zip`) sitting next to the binary, because
   on first run `initializeDatabase()` in `pkg/main.go` extracts `michelin.db`
   from `michelin.db.zip` **in the workflow's own directory**.

`pkg/buildApp.sh` only compiles + `lipo`s the universal binary — it does **not**
sign, notarize, or assemble the bundle. Those are the manual steps below.

## Prerequisites (one-time)

- Go toolchain with CGO (`CGO_ENABLED=1`) and the Xcode command-line tools.
- The **Developer ID Application** certificate in the login keychain:
  `Developer ID Application: Giovanni Coppola (VDG762YNX9)`
  (identity hash `C85516732AD930BB84E217AFF2C375E64268B111`).
  Check with `security find-identity -v -p codesigning`.
- A stored **notarytool keychain profile** named `gio-notarytool`. If it does not
  exist, create it once (uses an App Store Connect API key or app-specific
  password):
  ```bash
  xcrun notarytool store-credentials gio-notarytool
  ```

(Same signing setup as the AlfreDo workflow.)

## Release steps

Run from the repo root. Replace `<DB_PATH>` with the freshly scraped database.

### 1. Build the universal binary (and the db zip)

```bash
cd pkg
./buildApp.sh michelin ../source "<DB_PATH>/michelin.db"
cd ..
```

The 3rd argument tells the script to also produce `source/michelin.db.zip`
(it stages the file as `michelin.db` inside the archive, which is what the
runtime expects). This leaves both `source/michelin` and `source/michelin.db.zip`
in place.

### 2. Sign the binary

```bash
codesign --force --options runtime --timestamp \
  --sign C85516732AD930BB84E217AFF2C375E64268B111 \
  source/michelin

codesign -dvv source/michelin   # Authority must be "Developer ID Application…"
```

`--options runtime` (hardened runtime) is required for notarization.

### 3. Notarize

A bare Mach-O binary cannot be **stapled** (only `.app`/`.pkg`/`.dmg` can), so we
submit a zip and rely on online notarization — same as every prior release.

```bash
ditto -c -k --keepParent source/michelin /tmp/michelin-notarize.zip
xcrun notarytool submit /tmp/michelin-notarize.zip \
  --keychain-profile gio-notarytool --wait
# wait for: status: Accepted
```

Verify Gatekeeper acceptance:

```bash
spctl -a -vvv -t install source/michelin
# expect: accepted / source=Notarized Developer ID
```

### 4. Bump version + readme

In `source/info.plist`:
- `version` → the new version string.
- Update the bundled `readme` text and its "New in version X" section.

### 5. Package the `.alfredworkflow`

Make sure the **signed** binary and `michelin.db.zip` are both present in the
workflow folder, then build the bundle. Two ways:

- **Export from Alfred** (Workflow → ⋯ → Export). ⚠️ Alfred exports whatever is in
  the *installed* workflow folder — if `michelin.db.zip` is not in that folder the
  export will be missing the database. After exporting, confirm (step 6); if the
  db is missing, inject it:
  ```bash
  zip -j releases/MichelinGuide_<VER>.alfredworkflow source/michelin.db.zip
  ```
- **Or zip `source/` directly** (the bundle is a flat zip of the workflow files):
  ```bash
  ( cd source && zip -r -X "../releases/MichelinGuide_<VER>.alfredworkflow" . \
      -x '.DS_Store' 'prefs.plist' )
  ```

### 6. Verify the final archive

```bash
unzip -l releases/MichelinGuide_<VER>.alfredworkflow   # expect michelin + michelin.db.zip + info.plist
# ~33 MB total; a ~16 MB archive means the db is MISSING.

# Spot-check the embedded binary and database:
rm -rf /tmp/relcheck && mkdir /tmp/relcheck
unzip -oq releases/MichelinGuide_<VER>.alfredworkflow -d /tmp/relcheck
codesign -dvv /tmp/relcheck/michelin                    # Developer ID, signed
spctl -a -vvv -t install /tmp/relcheck/michelin         # Notarized Developer ID
unzip -oq /tmp/relcheck/michelin.db.zip -d /tmp/relcheck/db
sqlite3 /tmp/relcheck/db/michelin.db \
  "SELECT COUNT(*) FROM restaurants; SELECT MAX(year) FROM restaurant_awards;"
```

Confirm the restaurant count / max award year match the version you intend to
ship — **not** a stale database.

### 7. Commit, push, tag, merge

Commit only the release files (don't sweep in unrelated working-tree changes):

```bash
git add README.md source/info.plist source/michelin \
        source/michelin.db.zip releases/MichelinGuide_<VER>.alfredworkflow
git commit -m "v<VER> release: …"
git tag v<VER>
git push origin <branch> --tags
# then fast-forward main and push
```

## Gotchas (learned the hard way)

- **Unsigned binary ships silently.** `buildApp.sh` produces an unsigned binary;
  it runs fine locally but Gatekeeper blocks fresh downloads. Always do steps 2–3.
- **Alfred export drops the db.** The `.alfredworkflow` will be ~16 MB and DB-less
  unless `michelin.db.zip` is in the workflow folder before export. Verify size.
- **Wrong/stale database.** Keep exactly one `michelin.db.zip` — the current one,
  in `source/`. A copy left at the repo root once turned out to be the old v0.1 DB
  (21,092 rows, awards to 2025) and nearly shipped. Always run the step-6
  spot-check.
- **You can't staple** a standalone binary; online notarization is expected.
