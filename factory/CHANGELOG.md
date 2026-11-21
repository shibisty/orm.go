# Changelog

## Unreleased

## 0.2.1 — 2026-11-21

### Changed
- Renamed from `orm-factory` to `@orm/factory` (gtr ADR-0012): `gtr add @orm/factory`, `import "orm/factory"`; published to the gtr registry. Dependencies on other orm and migration packages are registry ranges instead of `github:` sources.

### Added
- `factory.New` (a definition on faker) and `factory.Fill` (from `fake` tags); `Make`,
  `MakeMany`, `Create`, `CreateMany` through `orm.Repository`; `State`, `Sequence`,
  `Seed`, `Faker`, `AfterCreate`; related records with `factory.Has` and `factory.For`.
