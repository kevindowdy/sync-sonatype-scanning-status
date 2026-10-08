# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-08

### Added

- `sync-sonatype-scanning-status` tool: finds Non Compliant APM versions whose
  Sonatype scan looks stale in the Scanning & Compliance workbook, checks the
  Sonatype reports API for release-stage scans in the last 90 days, and writes
  the scan date back to the raw FIG Version Scans file.
- `sonatype` client (cookie or basic auth) and `spreadsheet` helpers for
  .xlsx and .csv files; new dependency `github.com/xuri/excelize/v2`.
- Initial project template: source layout, test scaffolding, CI/CD
  workflows, and contributor documentation.

## [0.0.0] - 2026-09-02

### Added

- Initial release of the template repository.
