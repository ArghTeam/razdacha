#!/bin/sh
# Установщик razdacha — селективного VPN-шлюза.
#
#   curl -fsSL https://raw.githubusercontent.com/ArghTeam/razdacha/main/packaging/install.sh | sh
#
# Порядок жёсткий и описан в docs/08-install-upgrade.md: сначала ВСЕ проверки,
# потом ВСЕ изменения. Если проверка не прошла, система не тронута вовсе — это
# главное свойство скрипта, и ради него первая фаза не имеет права ничего
# писать, ставить и запускать.
#
# Повторный запуск — обновление, а не переустановка: ключи сервера, пиры,
# туннели и правила сохраняются. Признак — наличие /var/lib/razdacha/state.db.
#
# Переменные окружения:
#   RAZDACHA_DRY_RUN=1            прогнать только фазу проверок и выйти
#   RAZDACHA_VERSION=v1.2.3       поставить конкретный релиз вместо своего
#   RAZDACHA_REPO=owner/name      откуда качать релиз
#   RAZDACHA_PREFIX=/usr/local/bin куда класть бинарники
#   RAZDACHA_PUBLIC=1             панель на всех интерфейсах (ADR 0009);
#                                 0 выключает публичный режим обратно,
#                                 не задана — режим остаётся прежним
#   RAZDACHA_PEER=имя             имя первого пира вместо client-1
#   RAZDACHA_ALLOW_UNSUPPORTED=1  не отказываться на дистрибутиве вне матрицы

set -eu

REPO="${RAZDACHA_REPO:-ArghTeam/razdacha}"
PREFIX="${RAZDACHA_PREFIX:-/usr/local/bin}"

# BUILT_TAG — тег, под которым выложена эта копия скрипта. Подставляется
# workflow релиза при копировании файла в ассеты; в репозитории он пустой, и
# запуск из рабочей копии ставит последний релиз, как раньше.
#
# Без этого install.sh из релиза v0.1.1 ставил v0.2.0: путь
# /releases/latest/download был зашит, и тег ассета ни на что не влиял.
BUILT_TAG=""

# Не VERSION: /etc/os-release определяет эту переменную сам, и после разбора
# дистрибутива тег релиза превратился бы в «12 (bookworm)».
RELEASE_TAG="${RAZDACHA_VERSION:-$BUILT_TAG}"
DRY_RUN="${RAZDACHA_DRY_RUN:-}"
STATE_DB="/var/lib/razdacha/state.db"
NGINX_SITE="/etc/nginx/sites-available/razdacha"
SINGBOX_CONFIG="/etc/sing-box/config.json"
MARKER="# razdacha:"
MIN_DISK_KB=102400 # 100 МБ

# Накопители первой фазы. Отказы копятся до конца, а не роняют скрипт на первом:
# показать разом всё, что не так, полезнее, чем гонять установку по кругу.
FAILURES=""
WARNINGS=""

say() { printf '%s\n' "$*"; }
step() { printf '\n==> %s\n' "$*"; }
fail() { FAILURES="${FAILURES}  ✗ $*
"; }
warn() { WARNINGS="${WARNINGS}  ! $*
"; }
die() {
	printf '\nОшибка: %s\n' "$*" >&2
	exit 1
}

have() { command -v "$1" >/dev/null 2>&1; }

# ---------------------------------------------------------------------------
# Фаза 1 — проверки. Ничего не устанавливает и не пишет на диск.
# ---------------------------------------------------------------------------

check_root() {
	if [ "$(id -u)" -ne 0 ]; then
		fail "нужен root: запустите через sudo"
	fi
}

check_systemd() {
	if [ ! -d /run/systemd/system ]; then
		fail "systemd не обнаружен: razdacha управляет своими сервисами через него"
	fi
	if ! have systemctl; then
		fail "не найден systemctl"
	fi
}

# Ядро ≥ 5.15 (docs/07-platform-support.md). Отсечка не произвольная: на 5.4
# WireGuard собирается через DKMS, и это отдельный класс отказов на VPS.
check_kernel() {
	release="$(uname -r)"
	major="$(printf '%s' "$release" | cut -d. -f1)"
	minor="$(printf '%s' "$release" | cut -d. -f2 | cut -d- -f1)"
	case "$major$minor" in
	*[!0-9]* | "")
		warn "версия ядра «$release» не разобрана, проверка пропущена"
		return
		;;
	esac
	if [ "$major" -lt 5 ] || { [ "$major" -eq 5 ] && [ "$minor" -lt 15 ]; }; then
		fail "ядро $release старше 5.15; поддерживаются Debian 12+ и Ubuntu 22.04+"
	fi
}

check_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) fail "архитектура $(uname -m) не поддерживается; есть сборки amd64 и arm64" ;;
	esac
}

# Матрица из docs/07-platform-support.md. Дистрибутив вне матрицы — отказ, а не
# предупреждение: молчаливая установка с последующей загадочной
# неработоспособностью худший исход из возможных.
check_distro() {
	if [ ! -r /etc/os-release ]; then
		warn "/etc/os-release не прочитан, дистрибутив не определён"
		return
	fi
	# Файл читается в подоболочке: он определяет NAME, VERSION и ещё десяток
	# имён, и хотя бы одно из них наверняка совпадёт с нашим.
	# shellcheck disable=SC1091
	OS_NAME="$(. /etc/os-release && printf '%s' "${PRETTY_NAME:-${ID:-неизвестно}}")"
	# shellcheck disable=SC1091
	os_key="$(. /etc/os-release && printf '%s:%s' "${ID:-}" "${VERSION_ID:-}")"
	case "$os_key" in
	debian:12 | debian:13 | ubuntu:22.04 | ubuntu:24.04 | ubuntu:26.04) ;;
	debian:* | ubuntu:*)
		warn "$OS_NAME вне проверенной матрицы; ставим, но багрепорты приоритетными не будут"
		;;
	*)
		if [ -n "${RAZDACHA_ALLOW_UNSUPPORTED:-}" ]; then
			warn "$OS_NAME вне матрицы, продолжаем по RAZDACHA_ALLOW_UNSUPPORTED"
		else
			fail "$OS_NAME не поддерживается; нужны Debian 12+ или Ubuntu 22.04+ (RAZDACHA_ALLOW_UNSUPPORTED=1 обходит проверку)"
		fi
		;;
	esac
}

# Самая частая причина неудачной установки — не старая ОС, а OpenVZ: там нельзя
# загружать модули ядра, и ни WireGuard, ни tproxy не появятся никогда.
check_virt() {
	have systemd-detect-virt || return 0
	virt="$(systemd-detect-virt || true)"
	case "$virt" in
	openvz)
		fail "ваш VPS на OpenVZ: модули ядра там не загружаются, нужен KVM, Xen или железо"
		;;
	lxc | lxc-libvirt)
		warn "контейнер $virt: работает при NET_ADMIN и доступном tun, но в поддерживаемые конфигурации не входит"
		;;
	esac
}

# Модули: единственная проверка первой фазы, которая меняет состояние ядра.
# Загрузка модуля обратима и ничего не настраивает, а без неё отказ вылезет уже
# после установки — когда система изменена.
check_modules() {
	if [ ! -e /dev/net/tun ]; then
		fail "нет /dev/net/tun — почти всегда это OpenVZ или контейнер без доступа к tun"
	fi
	for module in wireguard nft_tproxy nft_socket; do
		if [ -d "/sys/module/$module" ]; then
			continue
		fi
		if have modprobe && modprobe "$module" 2>/dev/null; then
			continue
		fi
		fail "модуль ядра $module не загружается; на OpenVZ это неисправимо"
	done
}

check_disk() {
	for dir in /var/lib /usr/local; do
		[ -d "$dir" ] || continue
		free_kb="$(df -Pk "$dir" | awk 'NR==2 {print $4}')"
		case "$free_kb" in
		*[!0-9]* | "")
			warn "свободное место в $dir не определено"
			continue
			;;
		esac
		if [ "$free_kb" -lt "$MIN_DISK_KB" ]; then
			fail "в $dir свободно $((free_kb / 1024)) МБ, нужно не меньше $((MIN_DISK_KB / 1024)) МБ"
		fi
	done
}

check_tools() {
	if ! have curl && ! have wget; then
		fail "не найдены ни curl, ни wget: релиз нечем скачать"
	fi
	have tar || fail "не найден tar"
}

# Конфликты (docs/07-platform-support.md, «Конфликты»). На обновлении свой же
# wg0 и свой же конфиг sing-box — норма, поэтому проверки завязаны на FRESH.
check_conflicts() {
	if [ "$FRESH" = yes ]; then
		if have ip && ip link show wg0 >/dev/null 2>&1; then
			fail "интерфейс wg0 уже существует и создан не нами; переименуйте его или снимите"
		fi
		if [ -f "$SINGBOX_CONFIG" ]; then
			fail "$SINGBOX_CONFIG уже есть: похоже, sing-box управляет другая панель (3x-ui, marzban)"
		fi
	fi

	if have systemctl && systemctl list-unit-files 'wg-quick@wg0.service' 2>/dev/null | grep -q 'wg-quick@wg0'; then
		if systemctl is-enabled wg-quick@wg0.service >/dev/null 2>&1; then
			fail "включён wg-quick@wg0: он займёт имя интерфейса, отключите его"
		fi
	fi

	if [ -f "$NGINX_SITE" ] && ! head -n 1 "$NGINX_SITE" | grep -q "^$MARKER"; then
		fail "$NGINX_SITE писали не мы: перезаписывать чужой конфиг nginx мы не будем"
	fi

	if have ufw && ufw status 2>/dev/null | grep -q '^Status: active'; then
		warn "включён ufw: политика DROP на forward перебьёт правила razdacha"
	fi
	if have systemctl && systemctl is-active firewalld >/dev/null 2>&1; then
		warn "работает firewalld: политика DROP на forward перебьёт правила razdacha"
	fi
	if have docker; then
		warn "в системе docker: он ставит свои правила forward, порядок цепочек стоит проверить после установки"
	fi
}

# Внешний адрес и WAN определяются здесь только для отчёта — в работу их берёт
# демон через маршрутизацию. Неудача не отказ: на домашней машине за NAT адрес
# и должен быть локальным.
check_network() {
	if ! have ip; then
		warn "нет утилиты ip, внешний адрес и WAN-интерфейс не показаны"
		return
	fi
	route="$(ip -4 route get 1.1.1.1 2>/dev/null || true)"
	if [ -z "$route" ]; then
		warn "маршрут наружу не найден: проверьте сеть до установки"
		return
	fi
	EXTERNAL_ADDR="$(printf '%s' "$route" | sed -n 's/.* src \([0-9.]*\).*/\1/p')"
	WAN_IFACE="$(printf '%s' "$route" | sed -n 's/.* dev \([^ ]*\).*/\1/p')"
	[ -n "$EXTERNAL_ADDR" ] || warn "внешний адрес не определён; задайте его в панели после установки"
}

phase_checks() {
	step "Проверки"

	if [ -f "$STATE_DB" ]; then
		FRESH=no
	else
		FRESH=yes
	fi

	check_root
	check_systemd
	check_kernel
	check_arch
	check_distro
	check_virt
	check_modules
	check_disk
	check_tools
	check_conflicts
	check_network

	say "  ОС:              ${OS_NAME:-неизвестно}"
	say "  Ядро:            $(uname -r)"
	say "  Архитектура:     ${ARCH:-неизвестно}"
	say "  Внешний адрес:   ${EXTERNAL_ADDR:-не определён}"
	say "  WAN-интерфейс:   ${WAN_IFACE:-не определён}"
	if [ "$FRESH" = yes ]; then
		say "  Режим:           первая установка"
	else
		say "  Режим:           обновление, состояние в $STATE_DB сохраняется"
	fi

	if [ -n "$WARNINGS" ]; then
		printf '\nПредупреждения:\n%s' "$WARNINGS"
	fi
	if [ -n "$FAILURES" ]; then
		printf '\nУстановка невозможна:\n%s' "$FAILURES"
		die "система не изменена"
	fi
	say ""
	say "  Проверки пройдены."
}

# ---------------------------------------------------------------------------
# Фаза 2 — изменения.
# ---------------------------------------------------------------------------

download() {
	url="$1"
	dst="$2"
	if have curl; then
		curl -fsSL -o "$dst" "$url"
	else
		wget -q -O "$dst" "$url"
	fi
}

# release_url собирает адрес ассета. Имена ассетов без версии не случайность:
# только так работает /releases/latest/download, и скрипту не нужно ходить в
# API GitHub, чтобы узнать номер последнего релиза.
release_url() {
	if [ -n "$RELEASE_TAG" ]; then
		printf 'https://github.com/%s/releases/download/%s/%s' "$REPO" "$RELEASE_TAG" "$1"
	else
		printf 'https://github.com/%s/releases/latest/download/%s' "$REPO" "$1"
	fi
}

install_nginx() {
	if [ -d /etc/nginx/sites-available ]; then
		return
	fi
	step "Установка nginx"
	have apt-get || die "нет nginx и нет apt-get, поставьте nginx вручную"
	DEBIAN_FRONTEND=noninteractive apt-get update
	DEBIAN_FRONTEND=noninteractive apt-get install -y nginx
}

install_binaries() {
	step "Загрузка razdacha (${RELEASE_TAG:-последний релиз}, linux/$ARCH)"

	tmp="$(mktemp -d)"
	# Каталог убирается в любом исходе: сюда приезжает архив на десятки мегабайт.
	trap 'rm -rf "$tmp"' EXIT INT TERM

	asset="razdacha-linux-$ARCH.tar.gz"
	download "$(release_url "$asset")" "$tmp/$asset"

	# Контрольные суммы проверяются, если в релизе есть checksums.txt и в
	# системе sha256sum. Молчаливого «не смогли проверить, ставим» нет: об этом
	# говорится вслух.
	if have sha256sum && download "$(release_url checksums.txt)" "$tmp/checksums.txt"; then
		(cd "$tmp" && grep " $asset\$" checksums.txt | sha256sum -c -) ||
			die "контрольная сумма $asset не сошлась, установка прервана"
		say "  Контрольная сумма сошлась."
	else
		say "  ! контрольные суммы не проверены"
	fi

	tar -xzf "$tmp/$asset" -C "$tmp"
	mkdir -p "$PREFIX"
	for binary in razdachad razdacha; do
		[ -f "$tmp/$binary" ] || die "в архиве нет файла $binary"
		# Через временное имя и mv, а не записью поверх: на обновлении
		# razdachad работает, и открыть его файл на запись ядро не даст
		# (ETXTBSY). Переименование в том же каталоге атомарно, а запущенный
		# процесс продолжает жить со старым inode до перезапуска.
		install -m 0755 "$tmp/$binary" "$PREFIX/.$binary.new"
		mv -f "$PREFIX/.$binary.new" "$PREFIX/$binary"
		say "  $PREFIX/$binary"
	done

	rm -rf "$tmp"
	trap - EXIT INT TERM
}

# Всё остальное — sing-box, sysctl, ключи, БД, сертификат, конфиг nginx, юниты,
# первый пир и фаза вывода — делает сам бинарник: там уже есть проверенный код,
# а повторять его на shell значило бы завести вторую реализацию с другими
# ошибками.
run_setup() {
	step "Настройка"
	# Именно `if`, а не `[ … ] && set --`: при `set -eu` AND-список с ложным
	# условием возвращает единицу, и скрипт молча обрывался бы на первом же
	# незаданном необязательном параметре.
	set -- setup --daemon "$PREFIX/razdachad"
	# Три состояния, а не два: переменной нет — режим панели остаётся тем,
	# который сохранён в БД; переменная есть — она и решает. Раньше состояний
	# было два, и обновление без переменной молча возвращало панель из
	# публичного режима в приватный (issue #81).
	if [ -n "${RAZDACHA_PUBLIC+set}" ]; then
		case "$RAZDACHA_PUBLIC" in
		"" | 0 | no | No | NO | false | False | FALSE)
			set -- "$@" --public=false
			;;
		*)
			set -- "$@" --public=true
			;;
		esac
	fi
	if [ -n "${RAZDACHA_PEER:-}" ]; then
		set -- "$@" --peer "$RAZDACHA_PEER"
	fi
	"$PREFIX/razdacha" "$@"
}

main() {
	phase_checks

	if [ -n "$DRY_RUN" ]; then
		say ""
		say "  RAZDACHA_DRY_RUN — остановились после проверок, система не изменена."
		exit 0
	fi

	install_nginx
	install_binaries
	run_setup
}

# Справка печатается текстом, а не вырезается из шапки файла: при `curl … | sh`
# скрипта на диске нет вовсе, и читать из «$0» нечего.
show_help() {
	cat <<'HELP'
razdacha — установщик селективного VPN-шлюза.

  --dry-run   прогнать только фазу проверок и выйти, ничего не изменив

Переменные окружения:
  RAZDACHA_DRY_RUN=1             то же, что --dry-run
  RAZDACHA_VERSION=v1.2.3        поставить конкретный релиз вместо своего
  RAZDACHA_REPO=owner/name       откуда качать релиз
  RAZDACHA_PREFIX=/usr/local/bin куда класть бинарники
  RAZDACHA_PUBLIC=1              панель на всех интерфейсах; 0 — обратно в VPN;
                                 не задана — режим остаётся прежним
  RAZDACHA_PEER=имя              имя первого пира вместо client-1
  RAZDACHA_ALLOW_UNSUPPORTED=1   не отказываться на дистрибутиве вне матрицы
HELP
}

# `--dry-run` аргументом — то же, что RAZDACHA_DRY_RUN=1: через `curl | sh`
# аргументы передаются только как `sh -s -- --dry-run`, а на стенде скрипт
# запускают файлом, и там так короче.
parse_args() {
	for arg in "$@"; do
		case "$arg" in
		--dry-run) DRY_RUN=1 ;;
		-h | --help)
			show_help
			exit 0
			;;
		*) die "неизвестный аргумент $arg" ;;
		esac
	done
}

# Точка входа отделена от определений: тесты подгружают скрипт через `.` с
# RAZDACHA_SOURCE_ONLY=1 и проверяют выбор версии, не запуская ни одной проверки
# и ничего не трогая на машине.
if [ -z "${RAZDACHA_SOURCE_ONLY:-}" ]; then
	parse_args "$@"
	main
fi
