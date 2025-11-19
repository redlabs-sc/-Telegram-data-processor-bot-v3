#!/bin/bash

#===============================================================================
# run.sh - Telegram Data Processor Bot Launch Script
#
# This script starts the Telegram Data Processor Bot and tails the logs.
#
# Supported OS: Debian, Ubuntu, Kali Linux
#
# Usage: ./run.sh [options]
#   Options:
#     --build     Rebuild before running
#     --daemon    Run in background (daemonized)
#     --docker    Run using Docker Compose
#     --stop      Stop running bot
#     --status    Check bot status
#     --help      Show this help
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

#-------------------------------------------------------------------------------
# Get script directory
#-------------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

#-------------------------------------------------------------------------------
# Configuration
#-------------------------------------------------------------------------------
PID_FILE="$SCRIPT_DIR/coordinator.pid"
LOG_FILE="$SCRIPT_DIR/logs/coordinator.log"
BINARY="$SCRIPT_DIR/coordinator"

#-------------------------------------------------------------------------------
# Detect OS
#-------------------------------------------------------------------------------
detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS_NAME=$ID
        OS_PRETTY=$PRETTY_NAME
    elif [ -f /etc/lsb-release ]; then
        . /etc/lsb-release
        OS_NAME=$DISTRIB_ID
        OS_PRETTY=$DISTRIB_DESCRIPTION
    else
        OS_NAME="unknown"
        OS_PRETTY="Unknown OS"
    fi

    # Normalize OS name
    OS_NAME=$(echo "$OS_NAME" | tr '[:upper:]' '[:lower:]')

    case "$OS_NAME" in
        ubuntu|debian|kali)
            log_info "Detected: $OS_PRETTY"
            ;;
        *)
            log_warning "Unrecognized OS: $OS_NAME. Proceeding anyway..."
            ;;
    esac
}

#-------------------------------------------------------------------------------
# Check prerequisites
#-------------------------------------------------------------------------------
check_prerequisites() {
    log_step "Checking prerequisites"

    local errors=0

    # Check Go
    if ! command -v go &> /dev/null && [ ! -x /usr/local/go/bin/go ]; then
        log_error "Go is not installed. Run setup.sh first."
        errors=$((errors + 1))
    fi

    # Check .env file
    if [ ! -f "$SCRIPT_DIR/.env" ]; then
        log_error ".env file not found"
        if [ -f "$SCRIPT_DIR/.env.example" ]; then
            log_info "Creating .env from .env.example..."
            cp "$SCRIPT_DIR/.env.example" "$SCRIPT_DIR/.env"
            log_warning "Please edit .env with your configuration"
            errors=$((errors + 1))
        else
            error_exit "No .env or .env.example found. Run setup.sh first."
        fi
    fi

    # Check for bot token
    if grep -q "TELEGRAM_BOT_TOKEN=your_bot_token_here" "$SCRIPT_DIR/.env"; then
        log_error "TELEGRAM_BOT_TOKEN is not set in .env"
        log_info "Please edit .env and set your bot token"
        errors=$((errors + 1))
    fi

    # Check directories
    for dir in batches downloads logs; do
        if [ ! -d "$SCRIPT_DIR/$dir" ]; then
            log_info "Creating missing directory: $dir"
            mkdir -p "$SCRIPT_DIR/$dir"
        fi
    done

    if [ $errors -gt 0 ]; then
        error_exit "Prerequisites check failed with $errors error(s)"
    fi

    log_success "Prerequisites check passed"
}

#-------------------------------------------------------------------------------
# Check and start PostgreSQL
#-------------------------------------------------------------------------------
check_postgresql() {
    log_step "Checking PostgreSQL"

    # Check if PostgreSQL is installed
    if ! command -v psql &> /dev/null; then
        error_exit "PostgreSQL is not installed. Run setup.sh first."
    fi

    # Check if PostgreSQL is running
    if ! pg_isready -q 2>/dev/null; then
        log_warning "PostgreSQL is not running. Attempting to start..."

        # Try different methods to start PostgreSQL
        if command -v systemctl &> /dev/null; then
            sudo systemctl start postgresql 2>/dev/null || true
        fi

        if ! pg_isready -q 2>/dev/null; then
            sudo service postgresql start 2>/dev/null || true
        fi

        # Wait and check again
        local retries=5
        while ! pg_isready -q 2>/dev/null; do
            retries=$((retries - 1))
            if [ $retries -eq 0 ]; then
                error_exit "Cannot start PostgreSQL. Please start it manually."
            fi
            log_info "Waiting for PostgreSQL..."
            sleep 2
        done
    fi

    log_success "PostgreSQL is running"

    # Check database exists
    DB_NAME=$(grep "^DB_NAME=" "$SCRIPT_DIR/.env" | cut -d= -f2 | tr -d '"' | tr -d "'")
    DB_NAME=${DB_NAME:-telegram_bot_option2}

    if ! sudo -u postgres psql -lqt 2>/dev/null | cut -d \| -f 1 | grep -qw "$DB_NAME"; then
        log_warning "Database '$DB_NAME' not found"
        log_info "Run setup.sh to create the database"
    fi
}

#-------------------------------------------------------------------------------
# Build the project
#-------------------------------------------------------------------------------
build_project() {
    log_step "Building the project"

    cd "$SCRIPT_DIR"

    # Make sure Go is in PATH
    export PATH=$PATH:/usr/local/go/bin

    # Check for go.mod
    if [ ! -f "go.mod" ]; then
        error_exit "go.mod not found. Are you in the project directory?"
    fi

    # Build coordinator
    log_info "Building coordinator..."
    cd cmd/coordinator

    if ! go build -o "$BINARY" . 2>&1; then
        log_error "Build failed"

        # Try to fix common issues
        log_info "Attempting to fix: running go mod tidy..."
        cd "$SCRIPT_DIR"
        go mod tidy 2>&1 || true

        # Retry build
        cd cmd/coordinator
        if ! go build -o "$BINARY" . 2>&1; then
            error_exit "Build failed after attempted fix. Check dependencies."
        fi
    fi

    cd "$SCRIPT_DIR"
    log_success "Build successful: $BINARY"
}

#-------------------------------------------------------------------------------
# Check if bot is already running
#-------------------------------------------------------------------------------
is_running() {
    if [ -f "$PID_FILE" ]; then
        local pid=$(cat "$PID_FILE")
        if ps -p "$pid" > /dev/null 2>&1; then
            return 0
        else
            # Stale PID file
            rm -f "$PID_FILE"
        fi
    fi
    return 1
}

#-------------------------------------------------------------------------------
# Get running PID
#-------------------------------------------------------------------------------
get_pid() {
    if [ -f "$PID_FILE" ]; then
        cat "$PID_FILE"
    else
        echo ""
    fi
}

#-------------------------------------------------------------------------------
# Stop the bot
#-------------------------------------------------------------------------------
stop_bot() {
    log_step "Stopping bot"

    if ! is_running; then
        log_info "Bot is not running"
        return 0
    fi

    local pid=$(get_pid)
    log_info "Stopping bot (PID: $pid)..."

    # Send SIGTERM
    kill -TERM "$pid" 2>/dev/null || true

    # Wait for graceful shutdown
    local retries=10
    while ps -p "$pid" > /dev/null 2>&1; do
        retries=$((retries - 1))
        if [ $retries -eq 0 ]; then
            log_warning "Bot did not stop gracefully. Sending SIGKILL..."
            kill -KILL "$pid" 2>/dev/null || true
            break
        fi
        sleep 1
    done

    rm -f "$PID_FILE"
    log_success "Bot stopped"
}

#-------------------------------------------------------------------------------
# Show status
#-------------------------------------------------------------------------------
show_status() {
    log_step "Bot Status"

    if is_running; then
        local pid=$(get_pid)
        log_success "Bot is running (PID: $pid)"

        # Show resource usage
        if command -v ps &> /dev/null; then
            echo ""
            echo "Resource Usage:"
            ps -p "$pid" -o pid,ppid,%cpu,%mem,etime,args --no-headers 2>/dev/null || true
        fi

        # Check health endpoint
        echo ""
        log_info "Checking health endpoint..."
        if command -v curl &> /dev/null; then
            if curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/health | grep -q "200"; then
                log_success "Health endpoint: OK"
            else
                log_warning "Health endpoint: Not responding"
            fi
        fi
    else
        log_warning "Bot is not running"
    fi

    # Check PostgreSQL
    echo ""
    if pg_isready -q 2>/dev/null; then
        log_success "PostgreSQL: Running"
    else
        log_warning "PostgreSQL: Not running"
    fi

    # Show recent logs
    if [ -f "$LOG_FILE" ]; then
        echo ""
        log_info "Recent logs (last 10 lines):"
        tail -n 10 "$LOG_FILE" 2>/dev/null || true
    fi
}

#-------------------------------------------------------------------------------
# Run with Docker
#-------------------------------------------------------------------------------
run_docker() {
    log_step "Running with Docker Compose"

    if ! command -v docker &> /dev/null; then
        error_exit "Docker is not installed. Install Docker or run without --docker"
    fi

    if [ ! -f "$SCRIPT_DIR/docker-compose.yml" ]; then
        error_exit "docker-compose.yml not found"
    fi

    # Check for docker compose command
    if docker compose version &> /dev/null; then
        COMPOSE_CMD="docker compose"
    elif command -v docker-compose &> /dev/null; then
        COMPOSE_CMD="docker-compose"
    else
        error_exit "docker-compose not found"
    fi

    log_info "Starting services..."
    $COMPOSE_CMD up -d

    log_success "Services started"
    log_info "Tailing logs (Ctrl+C to stop)..."
    echo ""

    $COMPOSE_CMD logs -f coordinator
}

#-------------------------------------------------------------------------------
# Run in foreground
#-------------------------------------------------------------------------------
run_foreground() {
    log_step "Starting bot in foreground"

    if is_running; then
        local pid=$(get_pid)
        log_warning "Bot is already running (PID: $pid)"
        log_info "Use './run.sh --stop' to stop it first"
        exit 1
    fi

    # Check binary exists
    if [ ! -f "$BINARY" ]; then
        log_warning "Binary not found. Building..."
        build_project
    fi

    # Make sure log directory exists
    mkdir -p "$(dirname "$LOG_FILE")"

    log_info "Starting coordinator..."
    log_info "Logs: $LOG_FILE"
    log_info "Health: http://localhost:8080/health"
    log_info "Metrics: http://localhost:9090/metrics"
    echo ""
    log_info "Press Ctrl+C to stop"
    echo ""

    # Run and tee to both console and log file
    "$BINARY" 2>&1 | tee -a "$LOG_FILE"
}

#-------------------------------------------------------------------------------
# Run as daemon
#-------------------------------------------------------------------------------
run_daemon() {
    log_step "Starting bot as daemon"

    if is_running; then
        local pid=$(get_pid)
        log_warning "Bot is already running (PID: $pid)"
        log_info "Use './run.sh --stop' to stop it first"
        exit 1
    fi

    # Check binary exists
    if [ ! -f "$BINARY" ]; then
        log_warning "Binary not found. Building..."
        build_project
    fi

    # Make sure log directory exists
    mkdir -p "$(dirname "$LOG_FILE")"

    log_info "Starting coordinator as daemon..."

    # Start in background
    nohup "$BINARY" >> "$LOG_FILE" 2>&1 &
    local pid=$!

    # Save PID
    echo "$pid" > "$PID_FILE"

    # Wait a moment and check if it's still running
    sleep 2

    if ps -p "$pid" > /dev/null 2>&1; then
        log_success "Bot started (PID: $pid)"
        log_info "Logs: $LOG_FILE"
        log_info "Health: http://localhost:8080/health"
        log_info "Metrics: http://localhost:9090/metrics"
        echo ""
        log_info "Use './run.sh --status' to check status"
        log_info "Use './run.sh --stop' to stop"
        echo ""

        # Tail logs
        log_info "Tailing logs (Ctrl+C to stop watching, bot will keep running)..."
        echo ""
        tail -f "$LOG_FILE"
    else
        rm -f "$PID_FILE"
        error_exit "Bot failed to start. Check logs: $LOG_FILE"
    fi
}

#-------------------------------------------------------------------------------
# Show help
#-------------------------------------------------------------------------------
show_help() {
    echo "Usage: ./run.sh [options]"
    echo ""
    echo "Options:"
    echo "  --build     Rebuild the project before running"
    echo "  --daemon    Run in background (daemonized)"
    echo "  --docker    Run using Docker Compose"
    echo "  --stop      Stop running bot"
    echo "  --status    Check bot status"
    echo "  --help      Show this help"
    echo ""
    echo "Examples:"
    echo "  ./run.sh              # Run in foreground"
    echo "  ./run.sh --daemon     # Run in background"
    echo "  ./run.sh --build      # Rebuild and run"
    echo "  ./run.sh --docker     # Run with Docker"
    echo "  ./run.sh --stop       # Stop the bot"
    echo "  ./run.sh --status     # Check status"
    echo ""
    echo "Logs: $LOG_FILE"
    echo "PID file: $PID_FILE"
}

#-------------------------------------------------------------------------------
# Main execution
#-------------------------------------------------------------------------------
main() {
    echo -e "${CYAN}========================================${NC}"
    echo -e "${CYAN}  Telegram Data Processor Bot          ${NC}"
    echo -e "${CYAN}  (Corrected Architecture)             ${NC}"
    echo -e "${CYAN}========================================${NC}"
    echo ""

    # Detect OS
    detect_os

    # Parse arguments
    DO_BUILD=false
    RUN_MODE="foreground"

    while [[ $# -gt 0 ]]; do
        case $1 in
            --build)
                DO_BUILD=true
                shift
                ;;
            --daemon)
                RUN_MODE="daemon"
                shift
                ;;
            --docker)
                RUN_MODE="docker"
                shift
                ;;
            --stop)
                stop_bot
                exit 0
                ;;
            --status)
                show_status
                exit 0
                ;;
            --help|-h)
                show_help
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                show_help
                exit 1
                ;;
        esac
    done

    # Run with Docker if specified
    if [ "$RUN_MODE" = "docker" ]; then
        run_docker
        exit 0
    fi

    # Check prerequisites
    check_prerequisites

    # Check PostgreSQL
    check_postgresql

    # Build if requested
    if [ "$DO_BUILD" = true ]; then
        build_project
    fi

    # Run based on mode
    case "$RUN_MODE" in
        daemon)
            run_daemon
            ;;
        foreground)
            run_foreground
            ;;
    esac
}

# Run main
main "$@"
