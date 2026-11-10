#!/usr/bin/env bash
# 在 mine 上准备独立用户、数据库和仅本机可访问的网页入口。
set -euo pipefail

app_dir=/srv/turtle-soup
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
if ! id turtlesoup >/dev/null 2>&1; then
  sudo useradd --system --home-dir "$app_dir" --shell /usr/sbin/nologin turtlesoup
fi
sudo install -d -o turtlesoup -g turtlesoup -m 0755 "$app_dir"
sudo install -d -o turtlesoup -g turtlesoup -m 0700 "$app_dir/logs"
sudo install -d -m 0755 "$app_dir/releases"

if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='turtlesoup'" | grep -q 1; then
  sudo -u postgres createuser --login --no-superuser --no-createdb --no-createrole turtlesoup
fi
if ! sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='turtle_soup'" | grep -q 1; then
  sudo -u postgres createdb -O turtlesoup turtle_soup
fi

# 管理员初始密码只留在 SSH 登录用户的私有文件中，不进入仓库或输出。
password_dir="$HOME/.local/share/turtle-soup"
install -d -m 0700 "$password_dir"
if [ ! -f "$password_dir/admin-password" ]; then
  openssl rand -hex 18 > "$password_dir/admin-password"
  chmod 0600 "$password_dir/admin-password"
fi

sudo install -m 0644 "$script_dir/turtle-soup.service" /etc/systemd/system/turtle-soup.service
sudo install -m 0644 "$script_dir/turtle-soup.nginx.conf" /etc/nginx/sites-available/turtle-soup-private
sudo ln -sfn /etc/nginx/sites-available/turtle-soup-private /etc/nginx/sites-enabled/turtle-soup-private
sudo nginx -t
sudo systemctl daemon-reload
sudo systemctl enable turtle-soup
sudo systemctl reload nginx
