#!/bin/bash

# GPS Data Ingestor Deployment Script
# Deploys to Digital Ocean droplet

set -e

SERVER_IP="146.190.30.50"
SERVER_USER="root"
APP_NAME="gps-data-ingestor"
REMOTE_DIR="/opt/$APP_NAME"
LOCAL_DIR="$(pwd)"

echo "🚀 Starting deployment to $SERVER_IP..."

# Function to run commands on remote server
run_remote() {
    ssh -o StrictHostKeyChecking=no $SERVER_USER@$SERVER_IP "$1"
}

# Function to copy files to remote server
copy_to_remote() {
    scp -o StrictHostKeyChecking=no -r "$1" $SERVER_USER@$SERVER_IP:"$2"
}

echo "📦 Installing system dependencies..."
run_remote "
    # Update package list
    dnf update -y

    # Install Go and other dependencies
    dnf install -y golang

    # Create gps-ingestor user if it doesn't exist
    id -u gps-ingestor &>/dev/null || useradd -r -s /bin/false gps-ingestor

    echo 'System dependencies installed successfully'
"

echo "📁 Creating application directory on remote server..."
run_remote "
    if [ ! -d '$REMOTE_DIR' ]; then
        mkdir -p $REMOTE_DIR
        echo 'Created directory $REMOTE_DIR'
    else
        echo 'Directory $REMOTE_DIR already exists'
    fi
"

echo "📤 Copying application files to remote server..."
run_remote "rm -rf $REMOTE_DIR/*"

# Copy files using rsync (supports excludes) or individual scp commands
if command -v rsync >/dev/null 2>&1; then
    rsync -avz --exclude="data/*.txt" --exclude=".git" -e "ssh -o StrictHostKeyChecking=no" "$LOCAL_DIR/" $SERVER_USER@$SERVER_IP:"$REMOTE_DIR/"
else
    # Fallback to scp without excludes
    scp -o StrictHostKeyChecking=no -r "$LOCAL_DIR"/*.go "$LOCAL_DIR"/go.mod "$LOCAL_DIR"/configs "$LOCAL_DIR"/Dockerfile "$LOCAL_DIR"/docker-compose.yml $SERVER_USER@$SERVER_IP:"$REMOTE_DIR/"
fi

echo "🔨 Building Go application..."
run_remote "
    cd $REMOTE_DIR

    # Build the Go application
    go mod tidy
    go build -o main .

    # Make the binary executable and check permissions
    chmod +x main
    ls -la main

    # Create data directory and log directory, set permissions
    mkdir -p data
    mkdir -p /var/log/gps-ingestor

    # Set ownership of the entire directory and all files
    chown -R gps-ingestor:gps-ingestor $REMOTE_DIR /var/log/gps-ingestor

    # Ensure the binary is executable by the gps-ingestor user
    chmod 755 main
    chown gps-ingestor:gps-ingestor main

    # Debug: Check final permissions
    echo 'Final permissions:'
    ls -la main
    id gps-ingestor

    # Check and set SELinux context if SELinux is enabled
    if command -v getenforce >/dev/null 2>&1 && [ \$(getenforce) != 'Disabled' ]; then
        echo 'Setting SELinux context for binary'
        restorecon -v main
        chcon -t bin_t main 2>/dev/null || true
    fi

    # Install logrotate configuration
    cp configs/logrotate.conf /etc/logrotate.d/gps-ingestor

    echo 'Go application built successfully'
"

echo "🚫 Ensuring nginx is stopped..."
run_remote "
    # Stop and disable nginx (no longer needed)
    systemctl stop nginx || true
    systemctl disable nginx || true

    echo 'Nginx stopped and disabled'
"

echo "⚙️ Setting up systemd service..."
run_remote "
    # Copy service file
    cp $REMOTE_DIR/configs/gps-ingestor.service /etc/systemd/system/

    # Reload systemd and enable service
    systemctl daemon-reload
    systemctl enable gps-ingestor.service

    # Stop service if running, then start it
    systemctl stop gps-ingestor.service || true

    # Give the binary capability to bind to privileged ports as backup
    setcap 'cap_net_bind_service=+ep' $REMOTE_DIR/main || true

    systemctl start gps-ingestor.service

    echo 'Systemd service configured and started'
"

echo "🔍 Checking application status..."
run_remote "
    # Check service status
    systemctl status gps-ingestor.service --no-pager -l

    echo 'Waiting for service to be ready...'
    sleep 5

    # Check if service is running
    if systemctl is-active --quiet gps-ingestor.service; then
        echo '✅ GPS Ingestor service is running'
        echo 'TCP server listening on port 80'
    else
        echo '❌ GPS Ingestor service may not be running properly'
        echo 'GPS Ingestor service logs:'
        journalctl -u gps-ingestor.service --no-pager -l --since '5 minutes ago'
    fi
"

echo "🌐 Setting up firewall rules..."
run_remote "
    # Install and enable firewalld if not already installed
    dnf install -y firewalld
    systemctl enable firewalld
    systemctl start firewalld

    # Allow SSH (port 22)
    firewall-cmd --permanent --add-service=ssh

    # Allow HTTP (port 80)
    firewall-cmd --permanent --add-service=http

    # Allow HTTPS (port 443) for future use
    firewall-cmd --permanent --add-service=https

    # Reload firewall rules
    firewall-cmd --reload

    echo 'Firewall configured'
"

echo "✅ Deployment completed successfully!"
echo "🌍 TCP server is available at: $SERVER_IP:80"
echo "📡 Test connection: echo 'test data' | nc $SERVER_IP 80"
echo ""
echo "📋 Useful commands:"
echo "   SSH to server: ssh $SERVER_USER@$SERVER_IP"
echo "   View app logs: ssh $SERVER_USER@$SERVER_IP 'journalctl -u gps-ingestor.service -f'"
echo "   Restart app: ssh $SERVER_USER@$SERVER_IP 'systemctl restart gps-ingestor.service'"
echo "   Stop app: ssh $SERVER_USER@$SERVER_IP 'systemctl stop gps-ingestor.service'"
echo "   Check status: ssh $SERVER_USER@$SERVER_IP 'systemctl status gps-ingestor.service'"