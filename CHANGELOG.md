# Changelog

## [0.2.5](https://github.com/subhanmahmood/pgmanager/compare/v0.2.4...v0.2.5) (2026-09-03)


### Features

* lease keyed databases and add a scratch env ([#43](https://github.com/subhanmahmood/pgmanager/issues/43)) ([5e2f55f](https://github.com/subhanmahmood/pgmanager/commit/5e2f55f9aa67531a572870c313ea84b01fbf1e81))

## [0.2.4](https://github.com/subhanmahmood/pgmanager/compare/v0.2.3...v0.2.4) (2026-07-30)


### Features

* **web:** overhaul admin UI with React and Vite ([#37](https://github.com/subhanmahmood/pgmanager/issues/37)) ([9880e94](https://github.com/subhanmahmood/pgmanager/commit/9880e94d724330da31b2cc0543f153a2ace00110))


### Bug Fixes

* keep explorer table items readable ([#35](https://github.com/subhanmahmood/pgmanager/issues/35)) ([b0bf371](https://github.com/subhanmahmood/pgmanager/commit/b0bf371c9b0719726b6aa76cfae8db06aaf26b7a))

## [0.2.3](https://github.com/subhanmahmood/pgmanager/compare/v0.2.2...v0.2.3) (2026-07-28)


### Features

* **cli:** add `db rotate` to change a database user's password ([#32](https://github.com/subhanmahmood/pgmanager/issues/32)) ([2b32a6a](https://github.com/subhanmahmood/pgmanager/commit/2b32a6a8d94baa71b592c20f781ac84b2b014370))

## [0.2.2](https://github.com/subhanmahmood/pgmanager/compare/v0.2.1...v0.2.2) (2026-07-28)


### Features

* **cli:** store bearer tokens in the macOS Keychain ([#28](https://github.com/subhanmahmood/pgmanager/issues/28)) ([92d0260](https://github.com/subhanmahmood/pgmanager/commit/92d0260405ec567deef99616578bafb9a7dc8dff))
* **ui:** browse and edit database contents from the admin UI ([#31](https://github.com/subhanmahmood/pgmanager/issues/31)) ([0016cf5](https://github.com/subhanmahmood/pgmanager/commit/0016cf56e67218940f435243ed3c367c1920e71b))

## [0.2.1](https://github.com/subhanmahmood/pgmanager/compare/v0.2.0...v0.2.1) (2026-07-27)


### Features

* **auth:** sign in to the admin UI with an email and password ([#26](https://github.com/subhanmahmood/pgmanager/issues/26)) ([b3aed50](https://github.com/subhanmahmood/pgmanager/commit/b3aed5027cf1698c1517a70e2ea75edf70b7c7f6))

## [0.2.0](https://github.com/subhanmahmood/pgmanager/compare/v0.1.7...v0.2.0) (2026-07-27)


### ⚠ BREAKING CHANGES

* **auth:** credentials.yaml profiles with `postgres:`/`crypto:` blocks no longer work. Use `pgmanager login <api-url>` for a remote server, or run `pgmanager serve` with `api.socket` set and let the CLI find it.

### Features

* **auth:** device-code login, local admin socket, and removal of the direct-Postgres client ([#24](https://github.com/subhanmahmood/pgmanager/issues/24)) ([2dc159a](https://github.com/subhanmahmood/pgmanager/commit/2dc159a7052344f132d0a936e5a752caa97afc54))

## [0.1.7](https://github.com/subhanmahmood/pgmanager/compare/v0.1.6...v0.1.7) (2026-07-25)


### Features

* **web:** admin UI served on its own hostname ([#22](https://github.com/subhanmahmood/pgmanager/issues/22)) ([14e3c0c](https://github.com/subhanmahmood/pgmanager/commit/14e3c0ccf011bafaeb96c93e29f1a33769d902e0))

## [0.1.6](https://github.com/subhanmahmood/pgmanager/compare/v0.1.5...v0.1.6) (2026-05-25)


### Features

* **deploy:** add upgrade.sh wrapper for the canonical Deployment ([#18](https://github.com/subhanmahmood/pgmanager/issues/18)) ([20c11a7](https://github.com/subhanmahmood/pgmanager/commit/20c11a7b1ce079172be8572f1c5881daf406a1e6))
* **release:** publish Server image to ghcr.io and pin canonical Deployment ([#20](https://github.com/subhanmahmood/pgmanager/issues/20)) ([304887c](https://github.com/subhanmahmood/pgmanager/commit/304887c3cd3bce8db81de14a8d656ce54378bace))

## [0.1.5](https://github.com/subhanmahmood/pgmanager/compare/v0.1.4...v0.1.5) (2026-05-25)


### Bug Fixes

* **deploy:** wire POSTGRES_PUBLIC_HOST into the docker-compose stack ([#15](https://github.com/subhanmahmood/pgmanager/issues/15)) ([7323fd5](https://github.com/subhanmahmood/pgmanager/commit/7323fd57be1935ed45bf0b3ff5939b65584a6b41))

## [0.1.4](https://github.com/subhanmahmood/pgmanager/compare/v0.1.3...v0.1.4) (2026-05-25)


### Features

* **api:** advertise client-reachable host in db responses ([#13](https://github.com/subhanmahmood/pgmanager/issues/13)) ([9d4c25f](https://github.com/subhanmahmood/pgmanager/commit/9d4c25ff381fc2fb90a6ca026303a7e416424ba6))

## [0.1.3](https://github.com/subhanmahmood/pgmanager/compare/v0.1.2...v0.1.3) (2026-05-25)


### Features

* add `pgmanager update` self-update command ([#10](https://github.com/subhanmahmood/pgmanager/issues/10)) ([e0cf1cf](https://github.com/subhanmahmood/pgmanager/commit/e0cf1cfaeb1e833be056ac06bb2ad60443e407d6))

## [0.1.2](https://github.com/subhanmahmood/pgmanager/compare/v0.1.1...v0.1.2) (2026-05-25)


### Documentation

* document --extension flag in README CLI section ([5e859fe](https://github.com/subhanmahmood/pgmanager/commit/5e859febc37938662239b2a3dfb443cb869ee59a))

## [0.1.1](https://github.com/subhanmahmood/pgmanager/compare/v0.1.0...v0.1.1) (2026-05-24)


### Features

* **db:** allow installing extensions on create ([6d57fb7](https://github.com/subhanmahmood/pgmanager/commit/6d57fb7a880081d267b18715f35ce8a47b4ebff2))
* **db:** install Postgres extensions on db create with --extension/-x ([15b2609](https://github.com/subhanmahmood/pgmanager/commit/15b260981380adc1158c2378e6ee93f094758602))
