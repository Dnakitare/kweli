# Releasing

1. Make sure `main` is green (CI badge, or `go test ./... -count=1` locally).
2. Tag and push:
   ```
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```
3. `.github/workflows/release.yml` runs the test suite once more, then
   goreleaser builds darwin/linux/windows × amd64/arm64 binaries and
   publishes them (plus checksums.txt) to the GitHub Release it creates
   for the tag.
4. Update the Homebrew formula in `Dnakitare/homebrew-tap`
   (`Formula/kweli.rb`) to point at the new tag and its source tarball's
   sha256:
   ```
   VERSION=0.1.0
   curl -sL "https://github.com/Dnakitare/kweli/archive/refs/tags/v${VERSION}.tar.gz" | shasum -a 256
   ```
   then edit the `url` and `sha256` lines in the formula to match, and
   push that change to `homebrew-tap`. (This is deliberately not
   automated via goreleaser's `brews:` integration — that needs a
   cross-repo PAT this tool doesn't otherwise require. It's one `curl` +
   two line edits; not worth the extra credential.)
5. `go install github.com/Dnakitare/kweli/cmd/kweli@latest` picks up the
   new tag automatically via the Go module proxy — nothing to do there.

`goreleaser release --snapshot --clean` builds everything locally
(unpublished) to sanity-check a config change before tagging for real.
