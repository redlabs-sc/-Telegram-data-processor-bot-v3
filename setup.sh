#!/bin/bash

#===============================================================================
# setup.sh - Telegram Data Processor Bot Setup Script
#
# This script installs all necessary dependencies and configures the environment
# for the Telegram Data Processor Bot (Corrected Architecture).
#
# Supported OS: Debian, Ubuntu, Kali Linux
#
# Usage: sudo ./setup.sh
#===============================================================================

set -e  # Exit on error

#-------------------------------------------------------------------------------
# Color codes for output
#-------------------------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

#-------------------------------------------------------------------------------
# Logging functions
#-------------------------------------------------------------------------------
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_step() {
    echo -e "\n${CYAN}==>${NC} ${CYAN}$1${NC}"
}

#-------------------------------------------------------------------------------
# Error handling
#-------------------------------------------------------------------------------
error_exit() {
    log_error "$1"
    exit 1
}

# Trap errors
trap 'error_exit "An error occurred on line $LINENO. Exit code: $?"' ERR

#-------------------------------------------------------------------------------
# Detect OS
#-------------------------------------------------------------------------------
detect_os() {
    log_step "Detecting Operating System"

    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS_NAME=$ID
        OS_VERSION=$VERSION_ID
        OS_PRETTY=$PRETTY_NAME
    elif [ -f /etc/lsb-release ]; then
        . /etc/lsb-release
        OS_NAME=$DISTRIB_ID
        OS_VERSION=$DISTRIB_RELEASE
        OS_PRETTY=$DISTRIB_DESCRIPTION
    else
        error_exit "Cannot detect operating system. /etc/os-release not found."
    fi

    # Normalize OS name
    OS_NAME=$(echo "$OS_NAME" | tr '[:upper:]' '[:lower:]')

    case "$OS_NAME" in
        ubuntu|debian|kali)
            log_success "Detected: $OS_PRETTY"
            ;;
        *)
            error_exit "Unsupported OS: $OS_NAME. This script supports Debian, Ubuntu, and Kali Linux."
            ;;
    esac

    # Detect architecture
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64)
            GO_ARCH="amd64"
            ;;
        aarch64|arm64)
            GO_ARCH="arm64"
            ;;
        armv7l)
            GO_ARCH="armv6l"
            ;;
        *)
            error_exit "Unsupported architecture: $ARCH"
            ;;
    esac

    log_info "Architecture: $ARCH (Go: $GO_ARCH)"
}

#-------------------------------------------------------------------------------
# Check root privileges
#-------------------------------------------------------------------------------
check_root() {
    if [ "$EUID" -ne 0 ]; then
        log_warning "This script requires root privileges for installing packages."
        log_info "Please run with: sudo ./setup.sh"
        exit 1
    fi
}

#-------------------------------------------------------------------------------
# Update package lists
#-------------------------------------------------------------------------------
update_packages() {
    log_step "Updating package lists"

    apt-get update -qq || {
        log_warning "Failed to update package lists. Trying to fix..."
        apt-get update --fix-missing || error_exit "Cannot update package lists"
    }

    log_success "Package lists updated"
}

#-------------------------------------------------------------------------------
# Install basic dependencies
#-------------------------------------------------------------------------------
install_basic_deps() {
    log_step "Installing basic dependencies"

    local deps="curl wget git build-essential ca-certificates gnupg lsb-release unzip"

    for dep in $deps; do
        if ! dpkg -l | grep -q "^ii  $dep "; then
            log_info "Installing $dep..."
            apt-get install -y -qq "$dep" || {
                log_warning "Failed to install $dep, retrying..."
                apt-get install -y --fix-broken "$dep" || error_exit "Cannot install $dep"
            }
        else
            log_info "$dep is already installed"
        fi
    done

    log_success "Basic dependencies installed"
}

#-------------------------------------------------------------------------------
# Install Go
#-------------------------------------------------------------------------------
install_go() {
    log_step "Installing Go"

    GO_VERSION="1.21.5"
    GO_INSTALLED_VERSION=""

    # Check if Go is already installed
    if command -v go &> /dev/null; then
        GO_INSTALLED_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
        log_info "Go $GO_INSTALLED_VERSION is already installed"

        # Check version
        if [ "$(printf '%s\n' "$GO_VERSION" "$GO_INSTALLED_VERSION" | sort -V | head -n1)" = "$GO_VERSION" ]; then
            log_success "Go version is sufficient ($GO_INSTALLED_VERSION >= $GO_VERSION)"
            return 0
        else
            log_warning "Go version $GO_INSTALLED_VERSION is older than required $GO_VERSION"
            log_info "Upgrading Go..."
        fi
    fi

    # Download and install Go
    GO_TAR="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    GO_URL="https://go.dev/dl/${GO_TAR}"

    log_info "Downloading Go $GO_VERSION..."
    cd /tmp

    if ! wget -q "$GO_URL" -O "$GO_TAR"; then
        # Try alternative mirror
        log_warning "Primary download failed, trying alternative..."
        curl -LO "$GO_URL" || error_exit "Cannot download Go"
    fi

    # Remove old installation
    rm -rf /usr/local/go

    # Extract
    log_info "Installing Go to /usr/local/go..."
    tar -C /usr/local -xzf "$GO_TAR" || error_exit "Cannot extract Go archive"

    # Clean up
    rm -f "$GO_TAR"

    # Add to PATH
    if ! grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null; then
        echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
        chmod +x /etc/profile.d/go.sh
    fi

    # Export for current session
    export PATH=$PATH:/usr/local/go/bin

    # Verify installation
    if ! /usr/local/go/bin/go version &> /dev/null; then
        error_exit "Go installation failed"
    fi

    log_success "Go $GO_VERSION installed successfully"
}

#-------------------------------------------------------------------------------
# Install PostgreSQL
#-------------------------------------------------------------------------------
install_postgresql() {
    log_step "Installing PostgreSQL"

    if command -v psql &> /dev/null; then
        PG_VERSION=$(psql --version | awk '{print $3}' | cut -d. -f1)
        log_info "PostgreSQL $PG_VERSION is already installed"
    else
        log_info "Installing PostgreSQL..."

        case "$OS_NAME" in
            ubuntu|kali)
                apt-get install -y -qq postgresql postgresql-contrib || error_exit "Cannot install PostgreSQL"
                ;;
            debian)
                apt-get install -y -qq postgresql postgresql-contrib || error_exit "Cannot install PostgreSQL"
                ;;
        esac
    fi

    # Start PostgreSQL service
    log_info "Starting PostgreSQL service..."
    systemctl start postgresql 2>/dev/null || service postgresql start || {
        log_warning "Cannot start PostgreSQL service automatically"
    }

    # Enable on boot
    systemctl enable postgresql 2>/dev/null || update-rc.d postgresql enable || true

    # Wait for PostgreSQL to be ready
    local retries=10
    while ! sudo -u postgres pg_isready -q 2>/dev/null; do
        retries=$((retries - 1))
        if [ $retries -eq 0 ]; then
            error_exit "PostgreSQL failed to start"
        fi
        log_info "Waiting for PostgreSQL to be ready..."
        sleep 2
    done

    log_success "PostgreSQL installed and running"
}

#-------------------------------------------------------------------------------
# Setup PostgreSQL database and user
#-------------------------------------------------------------------------------
setup_database() {
    log_step "Setting up PostgreSQL database"

    DB_NAME="telegram_bot_option2"
    DB_USER="bot_user"
    DB_PASSWORD="change_me_in_production"

    # Check if user exists
    if sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='$DB_USER'" | grep -q 1; then
        log_info "User '$DB_USER' already exists"
    else
        log_info "Creating user '$DB_USER'..."
        sudo -u postgres psql -c "CREATE USER $DB_USER WITH PASSWORD '$DB_PASSWORD';" || {
            log_warning "User may already exist, continuing..."
        }
    fi

    # Check if database exists
    if sudo -u postgres psql -lqt | cut -d \| -f 1 | grep -qw "$DB_NAME"; then
        log_info "Database '$DB_NAME' already exists"
    else
        log_info "Creating database '$DB_NAME'..."
        sudo -u postgres psql -c "CREATE DATABASE $DB_NAME OWNER $DB_USER;" || error_exit "Cannot create database"
    fi

    # Grant privileges
    sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE $DB_NAME TO $DB_USER;" || true

    # Grant schema permissions (required for PostgreSQL 15+)
    sudo -u postgres psql -d $DB_NAME -c "GRANT ALL ON SCHEMA public TO $DB_USER;" || true
    sudo -u postgres psql -d $DB_NAME -c "GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO $DB_USER;" || true
    sudo -u postgres psql -d $DB_NAME -c "GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO $DB_USER;" || true
    sudo -u postgres psql -d $DB_NAME -c "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO $DB_USER;" || true
    sudo -u postgres psql -d $DB_NAME -c "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO $DB_USER;" || true

    log_success "Database setup complete"
}

#-------------------------------------------------------------------------------
# Run database migrations
#-------------------------------------------------------------------------------
run_migrations() {
    log_step "Running database migrations"

    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    MIGRATION_DIR="$SCRIPT_DIR/database/migrations"

    if [ ! -d "$MIGRATION_DIR" ]; then
        log_warning "Migration directory not found: $MIGRATION_DIR"
        return 0
    fi

    DB_NAME="telegram_bot_option2"
    DB_USER="bot_user"
    DB_PASSWORD="change_me_in_production"

    # Run each migration file
    for migration in "$MIGRATION_DIR"/*.sql; do
        if [ -f "$migration" ]; then
            log_info "Running migration: $(basename "$migration")"
            PGPASSWORD=$DB_PASSWORD psql -h localhost -U $DB_USER -d $DB_NAME -f "$migration" 2>&1 || {
                log_warning "Migration $(basename "$migration") may have already been applied"
            }
        fi
    done

    log_success "Database migrations complete"
}

#-------------------------------------------------------------------------------
# Install Docker (optional)
#-------------------------------------------------------------------------------
install_docker() {
    log_step "Installing Docker (optional)"

    if command -v docker &> /dev/null; then
        DOCKER_VERSION=$(docker --version | awk '{print $3}' | tr -d ',')
        log_info "Docker $DOCKER_VERSION is already installed"
        return 0
    fi

    log_info "Installing Docker..."

    # Add Docker's official GPG key
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/$OS_NAME/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    chmod a+r /etc/apt/keyrings/docker.gpg

    # Add repository
    echo \
        "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$OS_NAME \
        $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
        tee /etc/apt/sources.list.d/docker.list > /dev/null

    # Install Docker
    apt-get update -qq
    apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin || {
        log_warning "Failed to install Docker from official repo, trying alternative..."
        apt-get install -y -qq docker.io docker-compose || {
            log_warning "Docker installation failed - you can use the bot without Docker"
            return 0
        }
    }

    # Start Docker
    systemctl start docker 2>/dev/null || service docker start || true
    systemctl enable docker 2>/dev/null || true

    log_success "Docker installed successfully"
}

#-------------------------------------------------------------------------------
# Setup environment file
#-------------------------------------------------------------------------------
setup_env_file() {
    log_step "Setting up environment file"

    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    ENV_FILE="$SCRIPT_DIR/.env"
    ENV_EXAMPLE="$SCRIPT_DIR/.env.example"

    if [ -f "$ENV_FILE" ]; then
        log_info ".env file already exists"

        # Check for required variables
        local missing_vars=0

        if ! grep -q "TELEGRAM_BOT_TOKEN=." "$ENV_FILE" || grep -q "TELEGRAM_BOT_TOKEN=your_bot_token_here" "$ENV_FILE"; then
            log_warning "TELEGRAM_BOT_TOKEN is not set in .env"
            missing_vars=1
        fi

        if ! grep -q "ADMIN_IDS=." "$ENV_FILE" || grep -q "ADMIN_IDS=123456789,987654321" "$ENV_FILE"; then
            log_warning "ADMIN_IDS may need to be updated in .env"
        fi

        if [ $missing_vars -eq 1 ]; then
            log_warning "Please update .env with your actual values"
        fi
    else
        if [ -f "$ENV_EXAMPLE" ]; then
            log_info "Creating .env from .env.example..."
            cp "$ENV_EXAMPLE" "$ENV_FILE"
            log_warning "Please edit .env and set your TELEGRAM_BOT_TOKEN and ADMIN_IDS"
        else
            log_warning ".env.example not found, creating minimal .env..."
            cat > "$ENV_FILE" << 'EOF'
TELEGRAM_BOT_TOKEN=your_bot_token_here
ADMIN_IDS=123456789
DB_HOST=localhost
DB_PORT=5432
DB_NAME=telegram_bot_option2
DB_USER=bot_user
DB_PASSWORD=change_me_in_production
DB_SSL_MODE=disable
MAX_DOWNLOAD_WORKERS=3
MAX_EXTRACT_WORKERS=1
MAX_CONVERT_WORKERS=1
MAX_STORE_WORKERS=5
BATCH_SIZE=10
BATCH_TIMEOUT_SEC=300
LOG_LEVEL=info
LOG_FORMAT=json
METRICS_PORT=9090
HEALTH_CHECK_PORT=8080
EOF
            log_warning "Please edit .env and set your TELEGRAM_BOT_TOKEN and ADMIN_IDS"
        fi
    fi

    # Set proper permissions
    chmod 600 "$ENV_FILE"

    log_success "Environment file setup complete"
}

#-------------------------------------------------------------------------------
# Install Go dependencies
#-------------------------------------------------------------------------------
install_go_deps() {
    log_step "Installing Go dependencies"

    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    cd "$SCRIPT_DIR"

    # Make sure Go is in PATH
    export PATH=$PATH:/usr/local/go/bin

    # Check for go.mod
    if [ ! -f "go.mod" ]; then
        error_exit "go.mod not found. Are you in the project directory?"
    fi

    log_info "Running go mod tidy..."
    go mod tidy 2>&1 || {
        log_warning "go mod tidy had some issues, trying go mod download..."
        go mod download 2>&1 || {
            log_warning "Some dependencies may not be available offline"
        }
    }

    log_success "Go dependencies installed"
}

#-------------------------------------------------------------------------------
# Build the project
#-------------------------------------------------------------------------------
build_project() {
    log_step "Building the project"

    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    cd "$SCRIPT_DIR"

    # Make sure Go is in PATH
    export PATH=$PATH:/usr/local/go/bin

    # Build coordinator
    log_info "Building coordinator..."
    cd cmd/coordinator

    if go build -o "$SCRIPT_DIR/coordinator" . 2>&1; then
        log_success "Build successful: $SCRIPT_DIR/coordinator"
    else
        log_warning "Build failed - this may be due to missing dependencies"
        log_info "You can try building manually later with: cd cmd/coordinator && go build"
    fi

    cd "$SCRIPT_DIR"
}

#-------------------------------------------------------------------------------
# Create required directories
#-------------------------------------------------------------------------------
create_directories() {
    log_step "Creating required directories"

    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

    local dirs=(
        "$SCRIPT_DIR/batches"
        "$SCRIPT_DIR/downloads"
        "$SCRIPT_DIR/logs"
        "$SCRIPT_DIR/archive/failed"
    )

    for dir in "${dirs[@]}"; do
        if [ ! -d "$dir" ]; then
            mkdir -p "$dir"
            log_info "Created directory: $dir"
        else
            log_info "Directory exists: $dir"
        fi
    done

    # Set permissions
    chmod 755 "$SCRIPT_DIR/batches" "$SCRIPT_DIR/downloads" "$SCRIPT_DIR/logs"

    log_success "Directories created"
}

#-------------------------------------------------------------------------------
# Verify installation
#-------------------------------------------------------------------------------
verify_installation() {
    log_step "Verifying installation"

    local errors=0

    # Check Go
    if command -v go &> /dev/null || [ -x /usr/local/go/bin/go ]; then
        log_success "✓ Go is installed"
    else
        log_error "✗ Go is not installed"
        errors=$((errors + 1))
    fi

    # Check PostgreSQL
    if command -v psql &> /dev/null; then
        log_success "✓ PostgreSQL is installed"

        # Check if service is running
        if sudo -u postgres pg_isready -q 2>/dev/null; then
            log_success "✓ PostgreSQL is running"
        else
            log_warning "⚠ PostgreSQL is not running"
        fi
    else
        log_error "✗ PostgreSQL is not installed"
        errors=$((errors + 1))
    fi

    # Check database
    DB_NAME="telegram_bot_option2"
    DB_USER="bot_user"
    if sudo -u postgres psql -lqt | cut -d \| -f 1 | grep -qw "$DB_NAME"; then
        log_success "✓ Database '$DB_NAME' exists"
    else
        log_warning "⚠ Database '$DB_NAME' not found"
    fi

    # Check .env
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    if [ -f "$SCRIPT_DIR/.env" ]; then
        log_success "✓ .env file exists"
    else
        log_warning "⚠ .env file not found"
    fi

    # Check coordinator binary
    if [ -f "$SCRIPT_DIR/coordinator" ]; then
        log_success "✓ Coordinator binary built"
    else
        log_warning "⚠ Coordinator binary not built"
    fi

    # Check Docker (optional)
    if command -v docker &> /dev/null; then
        log_success "✓ Docker is installed (optional)"
    else
        log_info "○ Docker is not installed (optional)"
    fi

    echo ""
    if [ $errors -eq 0 ]; then
        log_success "Installation verification complete!"
    else
        log_warning "Installation completed with $errors error(s)"
    fi

    return $errors
}

#-------------------------------------------------------------------------------
# Print summary
#-------------------------------------------------------------------------------
print_summary() {
    echo ""
    echo -e "${CYAN}========================================${NC}"
    echo -e "${CYAN}       Setup Complete Summary           ${NC}"
    echo -e "${CYAN}========================================${NC}"
    echo ""
    echo -e "${GREEN}Next Steps:${NC}"
    echo ""
    echo "1. Edit your configuration:"
    echo "   nano .env"
    echo ""
    echo "2. Set your Telegram Bot Token:"
    echo "   TELEGRAM_BOT_TOKEN=your_actual_token"
    echo ""
    echo "3. Set your Admin User IDs:"
    echo "   ADMIN_IDS=your_telegram_user_id"
    echo ""
    echo "4. Run the bot:"
    echo "   ./run.sh"
    echo ""
    echo "5. Or run with Docker:"
    echo "   docker-compose up -d"
    echo ""
    echo -e "${YELLOW}Important Notes:${NC}"
    echo "- Database: telegram_bot_option2"
    echo "- User: bot_user"
    echo "- Password: change_me_in_production (CHANGE THIS!)"
    echo ""
    echo -e "${CYAN}========================================${NC}"
}

#-------------------------------------------------------------------------------
# Main execution
#-------------------------------------------------------------------------------
main() {
    echo -e "${CYAN}========================================${NC}"
    echo -e "${CYAN}  Telegram Data Processor Bot Setup     ${NC}"
    echo -e "${CYAN}  (Corrected Architecture)              ${NC}"
    echo -e "${CYAN}========================================${NC}"
    echo ""

    # Check root
    check_root

    # Detect OS
    detect_os

    # Install dependencies
    update_packages
    install_basic_deps
    install_go
    install_postgresql

    # Setup database
    setup_database
    run_migrations

    # Install Docker (optional)
    install_docker

    # Project setup
    setup_env_file
    create_directories
    install_go_deps
    build_project

    # Verify
    verify_installation

    # Summary
    print_summary
}

# Run main
main "$@"
