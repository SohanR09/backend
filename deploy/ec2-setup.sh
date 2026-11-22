#!/usr/bin/env bash
# One-shot setup / redeploy of the geofence backend (Go API + PostgreSQL) on an AWS EC2 instance.
# Works on Ubuntu/Debian and Amazon Linux 2023. Safe to re-run: it pulls the latest code and rebuilds.
#
# Usage (on the EC2 instance):
#   bash ec2-setup.sh                                   # API on http://<public-ip>:8080
#   DOMAIN=api.example.com EMAIL=you@x.com bash ec2-setup.sh   # + Nginx + free HTTPS cert on port 443
#
# Optional variables:
#   REPO_URL        git repo to deploy   (default: https://github.com/SohanR09/backend.git)
#   APP_DIR         where to put it      (default: ~/backend)
#   BRANCH          branch to deploy     (default: main)
#   ALLOWED_ORIGIN  CORS origin, e.g. https://your-frontend.vercel.app (default: *)
#   API_KEY         require X-API-Key on requests (default: empty = no auth)
#   DOMAIN / EMAIL  enable Nginx reverse proxy + Let's Encrypt (DOMAIN's DNS A record must point at this server)
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/SohanR09/backend.git}"
APP_DIR="${APP_DIR:-$HOME/backend}"
BRANCH="${BRANCH:-main}"
DOMAIN="${DOMAIN:-}"
EMAIL="${EMAIL:-}"

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------- 1. Docker + Compose ----------
if command -v apt-get >/dev/null 2>&1; then PKG=apt; elif command -v dnf >/dev/null 2>&1; then PKG=dnf; else
  echo "Unsupported OS (need apt or dnf)"; exit 1; fi

if ! command -v docker >/dev/null 2>&1; then
  log "Installing Docker"
  if [ "$PKG" = apt ]; then
    sudo apt-get update -y && sudo apt-get install -y ca-certificates curl git openssl
    curl -fsSL https://get.docker.com | sudo sh          # installs docker + compose plugin
  else
    sudo dnf install -y docker git openssl
  fi
  sudo systemctl enable --now docker
fi

if ! sudo docker compose version >/dev/null 2>&1; then
  log "Installing Docker Compose plugin"
  sudo mkdir -p /usr/local/lib/docker/cli-plugins
  sudo curl -fsSL "https://github.com/docker/compose/releases/download/v2.29.7/docker-compose-linux-$(uname -m)" \
    -o /usr/local/lib/docker/cli-plugins/docker-compose
  sudo chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
fi
sudo usermod -aG docker "$USER" || true   # lets you run docker without sudo after next login
DC="sudo docker compose"

# ---------- 2. Small instances: add swap so the Go build doesn't run out of memory ----------
if [ "$(free -m | awk '/Mem:/{print $2}')" -lt 2000 ] && ! swapon --show | grep -q /swapfile; then
  log "Adding 2 GB swap (low-memory instance)"
  sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
  grep -q /swapfile /etc/fstab || echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab >/dev/null
fi

# ---------- 3. Code ----------
if [ -d "$APP_DIR/.git" ]; then
  log "Updating code in $APP_DIR"
  git -C "$APP_DIR" fetch --quiet origin "$BRANCH"
  git -C "$APP_DIR" checkout --quiet "$BRANCH"
  git -C "$APP_DIR" pull --ff-only --quiet origin "$BRANCH"
else
  log "Cloning $REPO_URL into $APP_DIR"
  git clone --branch "$BRANCH" "$REPO_URL" "$APP_DIR"
fi
cd "$APP_DIR"

# ---------- 4. .env (created once; never overwritten, so the DB password stays stable) ----------
if [ ! -f .env ]; then
  log "Creating .env with a random database password"
  cat > .env <<EOF
DB_USER=geofence
DB_PASSWORD=$(openssl rand -hex 24)
DB_NAME=geofence
PORT=8080
API_KEY=${API_KEY:-}
RATE_LIMIT_PER_SEC=50
RATE_LIMIT_BURST=100
TRUST_PROXY=$([ -n "$DOMAIN" ] && echo true || echo false)
ALLOWED_ORIGIN=${ALLOWED_ORIGIN:-*}
EOF
  chmod 600 .env
else
  log ".env already exists — keeping it (edit $APP_DIR/.env to change settings)"
fi

# ---------- 5. Build + start (Postgres data lives in the 'pgdata' Docker volume) ----------
log "Building and starting containers"
$DC up -d --build
sudo docker image prune -f >/dev/null

log "Waiting for the API to become healthy"
for i in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:8080/health >/dev/null 2>&1; then echo "API is up."; break; fi
  [ "$i" = 60 ] && { echo "API did not become healthy. Logs:"; $DC logs --tail=50 api; exit 1; }
  sleep 2
done

# ---------- 6. Optional: Nginx + HTTPS ----------
if [ -n "$DOMAIN" ]; then
  log "Configuring Nginx for $DOMAIN"
  if [ "$PKG" = apt ]; then sudo apt-get install -y nginx certbot python3-certbot-nginx
  else sudo dnf install -y nginx certbot python3-certbot-nginx; fi
  CONF_DIR=/etc/nginx/conf.d
  sudo tee "$CONF_DIR/geofence-api.conf" >/dev/null <<EOF
server {
    listen 80;
    server_name $DOMAIN;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # WebSocket (/ws/alerts)
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
    }
}
EOF
  sudo rm -f /etc/nginx/sites-enabled/default
  sudo nginx -t && sudo systemctl enable --now nginx && sudo systemctl reload nginx

  if [ -n "$EMAIL" ]; then
    log "Requesting HTTPS certificate"
    sudo certbot --nginx -d "$DOMAIN" -m "$EMAIL" --agree-tos --non-interactive --redirect
  else
    echo "No EMAIL given — skipping HTTPS. Run later: sudo certbot --nginx -d $DOMAIN"
  fi
fi

PUBLIC_IP=$(curl -fsS -m 3 https://checkip.amazonaws.com 2>/dev/null || echo "<public-ip>")
log "Done"
if [ -n "$DOMAIN" ]; then echo "API: https://$DOMAIN/health"; else echo "API: http://$PUBLIC_IP:8080/health"; fi
echo "Logs:    cd $APP_DIR && sudo docker compose logs -f api"
echo "Restart: cd $APP_DIR && sudo docker compose restart"
