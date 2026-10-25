#!/bin/bash

# Installs Vestt as a systemd service.
#
# Ported in behaviour from the upstream Jackett installer:
#   tmp/ref/Jackett-0.24.2900/install_service_systemd.sh
# Differences required by Vestt's release layout:
#   - all paths/user/service name are parameterized (see --help below);
#   - a dedicated unprivileged system user is created instead of deriving the
#     user from the binary owner (Jackett asks you to chown beforehand);
#   - no launcher wrapper is needed: Vestt has no self-updater, the unit
#     ExecStart points directly at the binary.
#
# Layout conventions (release tarball vestt-v<ver>-linux-<arch>.tar.gz):
#   the tarball unpacks to a directory containing ./vestt and this script;
#   run the script from inside that directory (root privileges required).
#
# Usage:
#   sudo bash install_service_systemd.sh [options]
#
# Options:
#   --user NAME            run the service as this system user (default: vestt;
#                          created as a system user if missing)
#   --group NAME           group of the service user (default: same as --user;
#                          created as a system group if missing)
#   --install-path DIR     where the binary is installed (default: /opt/vestt)
#   --data-dir DIR         Vestt data directory, holds vestt.db and logs
#                          (default: /var/lib/vestt; passed via --data-dir)
#   --service-name NAME    systemd unit name, with or without .service suffix
#                          (default: vestt)
#   --port N               HTTP listen port baked into ExecStart (default: 9117;
#                          pass 0 to make the service use the configured value)
#   -h | --help            show this help
#
# The script is idempotent: a running service is stopped, the unit file is
# rewritten, then the service is re-enabled and started.

# Setting up colors
BOLDRED="$(printf '\033[1;31m')"
BOLDGREEN="$(printf '\033[1;32m')"
NC="$(printf '\033[0m')" # No Color

error() {
    echo "${BOLDRED}ERROR${NC}: $*"
}

usage() {
    sed -n '3,33p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

# ---------------------------------------------------------------- defaults --

VESTT_USER="vestt"
VESTT_GROUP=""
VESTT_INSTALL_PATH="/opt/vestt"
VESTT_DATA_DIR="/var/lib/vestt"
VESTT_SERVICE_NAME="vestt"
VESTT_PORT="9117"

# ------------------------------------------------------------- arg parsing --

while [ $# -gt 0 ]; do
    case "$1" in
        --user)          [ $# -ge 2 ] || { error "missing argument for --user"; exit 1; }
                         VESTT_USER="$2"; shift 2 ;;
        --group)         [ $# -ge 2 ] || { error "missing argument for --group"; exit 1; }
                         VESTT_GROUP="$2"; shift 2 ;;
        --install-path)  [ $# -ge 2 ] || { error "missing argument for --install-path"; exit 1; }
                         VESTT_INSTALL_PATH="$2"; shift 2 ;;
        --data-dir)      [ $# -ge 2 ] || { error "missing argument for --data-dir"; exit 1; }
                         VESTT_DATA_DIR="$2"; shift 2 ;;
        --service-name)  [ $# -ge 2 ] || { error "missing argument for --service-name"; exit 1; }
                         VESTT_SERVICE_NAME="$2"; shift 2 ;;
        --port)          [ $# -ge 2 ] || { error "missing argument for --port"; exit 1; }
                         VESTT_PORT="$2"; shift 2 ;;
        -h|--help)       usage ;;
        *)               error "unknown option '$1' (see --help)"; exit 1 ;;
    esac
done

VESTT_GROUP="${VESTT_GROUP:-${VESTT_USER}}"
VESTT_SERVICE_NAME="${VESTT_SERVICE_NAME%.service}"

if ! [ "${VESTT_PORT}" -eq "${VESTT_PORT}" ] 2>/dev/null || [ "${VESTT_PORT}" -lt 0 ] \
    || [ "${VESTT_PORT}" -gt 65535 ]; then
    error "--port must be an integer between 0 and 65535"
    exit 1
fi

# Check if the install script is running as root
if [ "$EUID" -ne 0 ]; then
    error "Please run this script as root"
    exit 1
fi

# Check that we're running from a release directory (script next to the binary)
SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)" || exit 1
if [ ! -f "${SCRIPT_DIR}/vestt" ]; then
    error "Can not locate 'vestt' binary in '${SCRIPT_DIR}'."
    echo "Is the script in the right directory? It must sit next to the 'vestt'"
    echo "binary from the release tarball (vestt-v<ver>-linux-<arch>.tar.gz)."
    exit 1
fi

VESTT_SERVICE="${VESTT_SERVICE_NAME}.service"

# Check if the Vestt service is already running
echo "Checking if the service '${VESTT_SERVICE}' is running ..."
if systemctl is-active --quiet "${VESTT_SERVICE}"; then
    echo "Service '${VESTT_SERVICE}' is running"

    # Stop and unload the service
    if systemctl stop "${VESTT_SERVICE}"; then
        echo "Service '${VESTT_SERVICE}' stopped"
    else
        error "The service '${VESTT_SERVICE}' can not be stopped"
        exit 1
    fi
else
    echo "Service '${VESTT_SERVICE}' is not running"
fi

# Create the dedicated system group / user (idempotent)
if ! getent group "${VESTT_GROUP}" >/dev/null; then
    echo "Creating system group '${VESTT_GROUP}' ..."
    groupadd --system "${VESTT_GROUP}" || { error "Can not create group '${VESTT_GROUP}'"; exit 1; }
fi
if ! id -u "${VESTT_USER}" >/dev/null 2>&1; then
    NOLOGIN="/usr/sbin/nologin"
    [ -x "${NOLOGIN}" ] || NOLOGIN="/sbin/nologin"
    echo "Creating system user '${VESTT_USER}' (shell: ${NOLOGIN}) ..."
    useradd --system --gid "${VESTT_GROUP}" --home-dir "${VESTT_INSTALL_PATH}" \
        --no-create-home --shell "${NOLOGIN}" "${VESTT_USER}" \
        || { error "Can not create user '${VESTT_USER}'"; exit 1; }
fi

# Install the binary
echo "Installing '${SCRIPT_DIR}/vestt' to '${VESTT_INSTALL_PATH}/vestt' ..."
if ! mkdir -p "${VESTT_INSTALL_PATH}"; then
    error "Can not create '${VESTT_INSTALL_PATH}'"
    exit 1
fi
if ! install -m 0755 "${SCRIPT_DIR}/vestt" "${VESTT_INSTALL_PATH}/vestt"; then
    error "Can not install the binary to '${VESTT_INSTALL_PATH}'"
    exit 1
fi
if ! chown -R "${VESTT_USER}:${VESTT_GROUP}" "${VESTT_INSTALL_PATH}"; then
    error "Can not chown '${VESTT_INSTALL_PATH}' to '${VESTT_USER}:${VESTT_GROUP}'"
    exit 1
fi

# Prepare the data directory (holds vestt.db and logs)
if ! mkdir -p "${VESTT_DATA_DIR}"; then
    error "Can not create data directory '${VESTT_DATA_DIR}'"
    exit 1
fi
chown "${VESTT_USER}:${VESTT_GROUP}" "${VESTT_DATA_DIR}" \
    || { error "Can not chown '${VESTT_DATA_DIR}'"; exit 1; }
chmod 750 "${VESTT_DATA_DIR}" || { error "Can not chmod '${VESTT_DATA_DIR}'"; exit 1; }

# Write the systemd service descriptor
SERVICE_PATH="/etc/systemd/system/${VESTT_SERVICE}"
echo "Creating Vestt unit file in '${SERVICE_PATH}' ..."
cat > "${SERVICE_PATH}" <<EOL
[Unit]
Description=Vestt Daemon
Documentation=https://github.com/GitHubNull/Vestt
After=network.target

[Service]
SyslogIdentifier=${VESTT_SERVICE_NAME}
Restart=always
RestartSec=5
Type=simple
User=${VESTT_USER}
Group=${VESTT_GROUP}
WorkingDirectory=${VESTT_INSTALL_PATH}
ExecStart=${VESTT_INSTALL_PATH}/vestt --port ${VESTT_PORT} --data-dir ${VESTT_DATA_DIR}
TimeoutStopSec=30
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ReadWritePaths=${VESTT_DATA_DIR}

[Install]
WantedBy=multi-user.target

EOL
if [ $? -ne 0 ]; then
    error "Can not create the file '${SERVICE_PATH}'"
    echo "The UnitPath of systemd changes from one distribution to another. You may have to edit the script and change the path manually."
    exit 1
fi

echo "Installing Vestt service ..."
# Reload systemd daemon
if ! systemctl daemon-reload; then
    error "Can not reload systemd daemon"
    exit 1
fi

# Enable the service for following restarts
if ! systemctl enable "${VESTT_SERVICE}"; then
    error "Can not enable the service '${VESTT_SERVICE}'"
    exit 1
fi

# Run the service
if systemctl start "${VESTT_SERVICE}"; then
    echo "${BOLDGREEN}Service successfully installed and launched!${NC}"
    echo "  unit:        ${SERVICE_PATH}"
    echo "  binary:      ${VESTT_INSTALL_PATH}/vestt"
    echo "  data dir:    ${VESTT_DATA_DIR}"
    echo "  web UI:      http://localhost:${VESTT_PORT}/UI/Dashboard"
    echo "To uninstall: systemctl disable --now ${VESTT_SERVICE} && rm -v '${SERVICE_PATH}' && systemctl daemon-reload"
else
    error "Can not start the service '${VESTT_SERVICE}'"
    exit 1
fi
