# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-30

First public version.

### Added

- Project directory with full-text search, filters by category, status and technology,
  and sorting by date, likes or launch date.
- Project pages with Markdown description, links, YouTube / Vimeo / Loom embeds,
  technologies, AI tools, status, launch date, pricing, city, product languages
  and "looking for".
- Passwordless login with one-time email codes; sessions with "log out everywhere".
- Likes and comments (htmx, works without JavaScript).
- Public user profiles with username, bio and links; account deletion.
- Moderation for admins: hide projects and comments, ban users.
- Latvian, English and Russian interface with browser language detection.
- Sitemap with `hreflang`, RSS feed, Open Graph tags, `robots.txt`.
- Single-binary Go server with embedded assets and SQLite database.
- Multi-stage Dockerfile (`dev`, `test`, `lint`, `build`, `prod`) and a `FROM scratch` runtime image.
- `makers healthcheck` and `makers backup` commands.

[Unreleased]: https://github.com/aleksejs1/makers/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/aleksejs1/makers/releases/tag/v0.1.0
