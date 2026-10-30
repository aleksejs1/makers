# Contributing to makers.lv

Thanks for helping! This is a small community project, so every bug report,
translation fix and pull request matters.

## Ways to help

- **Report bugs and suggest features** using the [issue templates](https://github.com/aleksejs1/makers/issues/new/choose).
- **Improve translations** — the texts live in `ui/locales/*.toml`.
- **Write code** — look for issues labelled `good first issue` or `help wanted`.

For larger changes, please open an issue first so we can agree on the approach.

## Development setup

You need Go 1.26+ (or just Docker).

```bash
git clone https://github.com/aleksejs1/makers.git
cd makers
make run        # starts on http://localhost:8080, login codes are printed to the log
```

Or with hot reload and a local mail catcher:

```bash
make dev        # app on :8080, Mailpit on :8025
```

Useful commands:

```bash
make test       # go vet + tests with the race detector
make lint       # golangci-lint (or: make docker-lint)
make fmt        # gofmt
```

## Guidelines

### Code

- Keep it simple: standard library first, a new dependency needs a good reason.
- Format with `gofmt`; the linter config is in `.golangci.yml`.
- Add tests for new behaviour. Handler tests live in `internal/server/server_test.go`
  and run the whole app against a temporary database.
- Pages must work without JavaScript; htmx is a progressive enhancement.

### Database

- Schema changes go into a **new** file in `internal/db/migrations/` named
  `NNNN_description.sql`. Never edit a migration that has been released.

### Translations

- Every user-facing string goes through the locale files. Add new keys to
  **all three** files: `en.toml`, `lv.toml` and `ru.toml`. Tests fail if a key is missing.
- Plural messages are TOML tables and must stay at the end of the file. Use the
  CLDR categories of the language: `one`/`other` (English), `zero`/`one`/`other`
  (Latvian), `one`/`few`/`many`/`other` (Russian).
- In templates use `{{.T "key"}}` or, inside partials, `{{.V.T "key"}}`.

### Commits and pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org/) for messages,
  e.g. `feat: add project screenshots`, `fix(i18n): correct Latvian plural`.
- Keep pull requests focused; describe what and why, attach screenshots for UI changes.
- Add a line to `CHANGELOG.md` under **Unreleased** for user-visible changes.

## Releases

Maintainers tag releases as `vX.Y.Z` ([SemVer](https://semver.org)). The release workflow
builds multi-arch images and publishes them to `ghcr.io/aleksejs1/makers`.

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
