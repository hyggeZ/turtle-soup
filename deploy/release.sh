#!/usr/bin/env bash
# 上传构建产物后发布一个可回滚版本，失败时恢复旧版本。
set -euo pipefail

version=${1:?请传入版本号}
if [[ ! "$version" =~ ^[a-zA-Z0-9._-]+$ ]]; then
  echo '版本号包含非法字符' >&2
  exit 1
fi

app_dir=/srv/turtle-soup
source_dir="$HOME/.local/share/turtle-soup/uploads/$version"
release_dir="$app_dir/releases/$version"
test -f "$source_dir/turtle-server" && test ! -L "$source_dir/turtle-server"
test -f "$source_dir/turtle-admin" && test ! -L "$source_dir/turtle-admin"
test -f "$source_dir/web.tar.gz"

# 先以普通用户解包，再检查归档路径与符号链接。
if tar -tzf "$source_dir/web.tar.gz" | grep -E '(^/|(^|/)\.\.(/|$))' >/dev/null; then
  echo '网页归档包含非法路径' >&2
  exit 1
fi
mkdir -p "$source_dir/web"
tar -xzf "$source_dir/web.tar.gz" -C "$source_dir/web"
if find "$source_dir/web" -type l | grep -q .; then
  echo '网页归档包含符号链接' >&2
  exit 1
fi
test -f "$source_dir/web/index.html"

previous=$(readlink -f "$app_dir/current" || true)
sudo install -d -m 0755 "$release_dir/web"
sudo install -m 0755 "$source_dir/turtle-server" "$release_dir/turtle-server"
sudo install -m 0755 "$source_dir/turtle-admin" "$release_dir/turtle-admin"
sudo cp -a "$source_dir/web/." "$release_dir/web/"
sudo chown -R root:root "$release_dir"
sudo ln -sfn "$release_dir" "$app_dir/current.next"
sudo mv -Tf "$app_dir/current.next" "$app_dir/current"

ready=false
if sudo systemctl restart turtle-soup; then
  for attempt in {1..20}; do
    if sudo systemctl is-active --quiet turtle-soup && \
       curl --fail --silent http://127.0.0.1:18080/health >/dev/null && \
       curl --fail --silent http://127.0.0.1:18084/ >/dev/null; then
      ready=true
      break
    fi
    sleep 1
  done
fi
if [ "$ready" != true ]; then
  if [ -n "$previous" ]; then
    sudo ln -sfn "$previous" "$app_dir/current.next"
    sudo mv -Tf "$app_dir/current.next" "$app_dir/current"
    sudo systemctl restart turtle-soup
  fi
  echo '新版本未能启动，已尝试恢复旧版本' >&2
  exit 1
fi

# 已设置的管理员密码与会话不会在后续发版时重置。
if ! sudo -u turtlesoup psql -d turtle_soup -tAc "SELECT 1 FROM users WHERE username='king'" | grep -q 1; then
  password_file="$HOME/.local/share/turtle-soup/admin-password"
  export SUPER_ADMIN_PASSWORD
  SUPER_ADMIN_PASSWORD=$(cat "$password_file")
  sudo --preserve-env=SUPER_ADMIN_PASSWORD -u turtlesoup env \
    DATABASE_URL='host=/var/run/postgresql dbname=turtle_soup user=turtlesoup sslmode=disable' \
    "$app_dir/current/turtle-admin" >/dev/null
  unset SUPER_ADMIN_PASSWORD
fi
