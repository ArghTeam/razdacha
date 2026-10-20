---
type: invariants
title: Packaging
description: nginx, TLS, systemd, install/restore/uninstall, release archives and supported platforms
---

## Invariants
- Версия Go задаётся один раз — в `go.mod`; CI читает её через `go-version-file`, числом нигде не дублируется.
- `CGO_ENABLED=0` проверяется отдельным шагом CI, а не подразумевается: нарушение роняет job (ADR 0006).
- Линт в CI и `make lint` используют один и тот же корневой `.golangci.yml`; конфиг не передаётся флагом.
- Версия рантайма `sing-box` равна версии библиотеки из `go.mod`: CI берёт тег релиза оттуда и сверяет с `sing-box version`.
- Целевые платформы закрываются кросс-сборкой `linux/amd64` и `linux/arm64`, а не матрицей ОС раннера.
- Панель наружу открывает nginx, демон слушает только loopback (ADR 0008) — это не меняется и в публичном режиме: upstream обязан быть loopback в любой ветке.
- Публичный адрес и `0.0.0.0` в `SiteConfig` принимаются только при `Public` (ADR 0009); без флага работает прежний белый список «приватный, loopback или link-local». Режим — отдельное решение, а не следствие опечатки в адресе.
- В публичном режиме nginx слушает `443` и `80` на всех интерфейсах; отдельного `listen` на `10.8.0.1` не добавляется — он уже покрыт.
- `limit_req` висит только на `POST /api/login` (зона `razdacha_login`, `10r/m`, burst 5, статус 429). На `/` его быть не должно: один экран панели тянет десятки запросов. Это грубый отсекатель ботов, точная защита — блокировка по адресу в демоне.
- Внешний адрес определяется маршрутизацией (UDP-сокет без отправки пакетов), а не запросом к внешней службе; неудача — предупреждение в лог и работа на одном `10.8.0.1`, а не остановка установки.
- Конфиг nginx генерируется кодом из `SiteConfig`, а не копируется файлом-шаблоном; первая строка — маркер `# razdacha: …`, по нему свой файл отличается от чужого.
- Чужие файлы в `/etc/nginx` не перезаписываются и не удаляются: файл без маркера и symlink не на наш файл — повод для ошибки при установке и для пропуска при удалении.
- Установка снимает `sites-enabled/default`: пакет nginx включает его с `listen 80 default_server`, и панель торчит наружу мимо нашего конфига. Снимается только symlink на `sites-available/default`, сам файл пакета не трогается.
- Возврат дефолтного сайта при удалении идёт по явной отметке `/etc/razdacha/nginx-default-disabled.json`, а не по догадке о состоянии: нет отметки — не возвращаем.
- `net.ipv4.ip_nonlocal_bind=1` обязателен и лежит в `/etc/sysctl.d/99-razdacha.conf` рядом с `ip_forward`: без него nginx не стартует, пока демон не создал `wg0`, и связка не переживает перезагрузку. Порядок юнитов эту дыру не закрывает.
- Параметры ядра пишутся в drop-in (переживает перезагрузку) и сразу в `/proc/sys` (не ждать её). `sysctl` бинарником не вызывается — это обычные файлы.
- nginx после смены набора сайтов перезапускается (`restart`), а не перечитывается: снятый штатный сайт оставляет свои сокеты живыми до конца старых воркеров, и машина оказывается одновременно с открытым `0.0.0.0:80` и без слушающей панели.
- Юниты получают `enable` и `restart`, а не `enable --now`: на уже работающем юните `--now` не делает ничего, и обновление оставляло на диске новый бинарник, а в памяти старый процесс (issue #80). На первой установке разницы нет.
- Режим панели — свойство установки, а не аргумент запуска: он хранится в БД и переживает обновление. `--public`/`RAZDACHA_PUBLIC` задаёт и запоминает, `--public=false`/`RAZDACHA_PUBLIC=0` выключает, отсутствие флага оставляет сохранённое. Явный флаг записывается всегда, даже совпав с умолчанием: «выбрал приватный» и «не спрашивали» — разные состояния. Ключа в БД нет (установка от версии до 0.2.1) — режим выводится из лежащего конфига nginx по его же директиве `listen` и записывается; конфига нет — приватный, как на первой установке; конфиг не разобран — приватный, но в БД не пишется ничего и причина печатается в выводе. Смена и вывод режима печатаются в выводе установщика (issue #81).
- Разбор конфига nginx (`PublicFromConfig`) проверяется тестом от `Render`, а не от текста, вписанного руками: разъедутся — тест упадёт.
- Путь обновления проверяется только настоящим обновлением: `curl … | sh` с прошлой версии, в чистом окружении (`env -i` — оставшийся в сессии `RAZDACHA_PUBLIC` скрывает ровно тот дефект, который ищешь) и с воспроизведённым исходным состоянием БД, иначе миграция не сработает и проверка окажется пустой.
- Ассеты релиза именуются без версии — иначе не работает `/releases/latest/download`; скачивание анонимное, поэтому репозиторий обязан оставаться публичным.
- `install.sh`, выложенный ассетом релиза, ставит свою версию: workflow подставляет тег в `BUILT_TAG`. В репозитории `BUILT_TAG` пустой — оттуда ставится последний релиз; `RAZDACHA_VERSION` перебивает и то, и другое.
- Резервная копия БД называется версией, состояние которой в ней лежит (читается из БД до миграций), а не той, на которую обновляемся: копия — путь отката.
- При удалении сбрасывается `ip_nonlocal_bind`, но не `ip_forward`: маршрутизацию включают не только мы. Асимметрия обязана быть в выводе — команда обещает исходное состояние.
- Установка и удаление идемпотентны: неизменившийся конфиг не переписывается, symlink не дублируется, валидный сертификат не перевыпускается.
- Сертификат панели выпускается средствами Go (`crypto/x509`, `crypto/ecdsa`), не вызовом `openssl`; ключ `0600`, каталог `0700`, SAN — IP адреса панели (в публичном режиме `10.8.0.1` и внешний адрес), срок 10 лет. Не хватает в SAN хотя бы одного нужного адреса — сертификат перевыпускается.
- Корень файловой системы в установщике — поле `Installer.Root`. Тесты подменяют его `t.TempDir()`, в реальные `/etc` не пишет ни один тест.

- Only a genuinely fresh setup creates the first peer and checks/writes initial runtime config. It freezes a private bootstrap seed distinct from a committed network generation; the daemon publishes it under the guard and durably removes seed authority before releasing traffic. Missing DB plus orphan bootstrap/committed authority is refused. Existing-install setup never automatically recreates a deleted first peer or writes pending DB policy into runtime config (ADR 0023).
- Legacy installations without committed policy require explicit Apply through an out-of-band panel/SSH tunnel. Normal restarts recover committed policy/list contents while live database peer revocation remains authoritative. Corrupt authority stops under the guard.
- Restore stops and verifies both managed services under durable inhibitors, finalizes coherent current-destination recovery, quarantines runtime config and guards ingress before DB publication. Bootstrap authority is invalidated and synced before committed authority. Restored routing requires explicit Apply (ADR 0027).

## Key entry points
- `.github/workflows/ci.yml` — все автоматические проверки репозитория.
- `Makefile` — local build, test, lint and Linux cross-build commands. The dormant `deb` target references a missing nfpm configuration and is not a supported packaging path.
- `cmd/razdachad/main.go` — точка входа демона.
- `internal/packaging/nginx.go` — `SiteConfig.Validate` и `Render`: генерация конфига nginx.
- `internal/packaging/tls.go` — `EnsureCertificate`: самоподписанный сертификат панели.
- `internal/packaging/external.go` — `DetectExternalAddr`: внешний адрес сервера для SAN.
- `internal/packaging/install.go` — `Installer.Install`/`Uninstall`: файлы nginx на диске.
- `internal/packaging/default_site.go` — снятие и возврат штатного сайта Debian.
- `internal/packaging/sysctl.go` — drop-in с параметрами ядра и их применение.

- Native artifact history is excluded from database backups. Restore invalidates routing authority and requires supported sources to be fetched and explicitly applied again; it never reopens references to absent blobs. In-place startup with legacy v1 native authority preserves committed WG administrative settings but keeps client ingress guarded until explicit Apply. Corrupt v2 authority/artifacts stay guarded. ADR 0027 adds held acquisition and paused outgoing WireGuard to this recovery boundary.

## Safe restore contract (ADR 0027)

[ADR 0027](../../docs/decisions/0027-safe-cross-host-restore.md) and
[task 217](../../tasks/217-safe-cross-host-restore.md) define the implemented restore behavior below. Local validation and independent
review are recorded in the task; isolated network/staging acceptance is separate.

All-restores default, offline staging/classification, exact managed-unit capability preflight, durable dual-service inhibitors, runtime quarantine, coherent current-destination recovery and journal-safe replacement.

The same lifecycle lock covers managed installer binary publication through setup/start, with a verified inherited FD handoff. Inspect staged daemon/CLI capabilities before incompatible update/downgrade when any trusted restore state requires the protocol; no publication gap or version-label bypass.

Runtime start enforcement also uses a permanent managed ExecCondition calling
a metadata-only CLI check. Publishing permits only the exact candidate under a short
boot/operation/live-daemon-identity bound permit; stale reboot permits deny independently
of daemon startup order. Abort must stop/verify the candidate after durable inhibition,
not merely create a marker. A marker or expired permit alone never proves an already running process stopped.

Restore/recovery additionally require readable unified cgroup-v2 evidence and exact
managed execution/config topology before any safety/service/DB mutation. Both durable
inhibitors precede the first unit reload. Setup refuses any retained trusted restore
state even after a compatible binary update. Incompatible downgrade checks include
orphan permits and recovery manifests. Global reset and DB-mutating early flags inspect
the canonical installation under its stable lifecycle lock, including alternate-path
and symlink aliases. Explicit recovery restores the current destination, keeps prior
hold and enabled intent, and separates passive recovered state from acknowledged
activation. Even originally active runtime waits for explicit Apply.

Destination DB, WAL, SHM and rollback-journal admission precedes SQLite access and
initial restore safety/service mutation. The shared check requires a private canonical
parent and owner-controlled mode-0600 single-link regular files, rejects aliases and
orphan sidecars, and repeats in snapshot/checkpoint/replacement/recovery and protected
password writes. A rejected alias never changes its sibling target.
