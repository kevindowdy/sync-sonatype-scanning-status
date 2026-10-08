# golang-template

Template repository for new Go applications, providing a standard project
layout, tooling, and CI/CD configuration to build from.

## Description

This repository is not an application itself — it's a starting point.
Copy it (or use it as a GitHub template) when creating a new Go service or
CLI, then replace the placeholder code under `src/` with real application
logic.

## Setup Instructions

1. Ensure [Go](https://go.dev/dl/) 1.24 or later is installed.
2. Clone the repository:
   ```sh
   git clone https://github.com/kevindowdy/golang-template.git
   cd golang-template
   ```
3. Download dependencies:
   ```sh
   go mod download
   ```

## sync-sonatype-scanning-status

Workflow: run `process-scanning-data`, run this tool, then run
`process-scanning-data` again so the refreshed Sonatype dates flow into the
final report.

1. Reads the compliance workbook (`-compliance-file`) and keeps rows where
   `Ext Scanning` is Non Compliant and `SNT Age` is over 90 days (or blank),
   dropping rows already stale because of Fortify or WebInspect
   (`FOP AGE`/`WI AGE` over 90, or status Out of Scope). The `zvx`
   (`<APM> <version>`) value of each row is the key.
2. `GET`s the Sonatype reports endpoint, and for each key looks for a
   `release`-stage result whose report URL contains the key and whose
   `evaluationDate` is within 90 days.
3. Writes the newest such date (`YYYY-MM-DD`) into `Last_Sonatype_Scan_Dt` of
   the raw scans file (`-scans-file`, .csv or .xlsx) on rows where
   `AMAPM_Number` and `Release/Version` match. Columns are located by header
   name, not letter. Writes are atomic.

| Setting | Flag / env | Notes |
| --- | --- | --- |
| Compliance workbook | `-compliance-file` / `COMPLIANCE_FILE` | required |
| Raw scans file | `-scans-file` / `SCANS_FILE` | required |
| Sonatype endpoint | `-sonatype-url` / `SONATYPE_URL` | required; GET returning a JSON array of `{stage, evaluationDate, reportHtmlUrl}` |
| Cookie auth | `SONATYPE_COOKIE` | raw `Cookie` header value; wins over basic auth |
| Basic auth | `SONATYPE_USERNAME`, `SONATYPE_PASSWORD` | used when no cookie |
| Dry run | `-dry-run` | log changes without writing |

```sh
SONATYPE_COOKIE='...' go run ./src -compliance-file today.xlsx -scans-file scans.csv -sonatype-url https://iq.example.com/api/v2/reports/applications
```

## Run Instructions

Run the application directly:

```sh
go run ./src/cmd/app
```

Build a binary:

```sh
go build -o bin/app ./src/cmd/app
./bin/app
```

Run tests:

```sh
go test ./...
```

## Project Structure

```
src/            Application source code
  cmd/app/      Binary entrypoint (main package)
  internal/     Private packages not importable by other modules
tests/          Integration/end-to-end tests
.github/        CI/CD workflows and issue/PR templates
```

Unit tests live alongside the package they test (Go convention); `tests/`
holds broader integration tests.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Licensed under the terms in [LICENSE](LICENSE).
