#!/usr/bin/env bash
# 上传构建产物后发布一个可回滚版本，失败时恢复旧版本。
set -euo pipefail

version=${1:?请传入版本号}
if [[ ! "$version" =~ ^[a-zA-Z0-9._-]+$ || "$version" == . || "$version" == .. ]]; then
  echo '版本号包含非法字符' >&2
  exit 1
fi

app_dir=/srv/turtle-soup
source_dir="$HOME/.local/share/turtle-soup/uploads/$version"
releases_dir=$(readlink -e -- "$app_dir/releases")
release_dir="$releases_dir/$version"

# 所有发布者共用 releases 目录上的锁，直到切换、探活和回退完成。
exec 9<"$releases_dir"
if ! flock -n 9; then
  echo '已有发布任务正在运行' >&2
  exit 1
fi
if [[ -e "$release_dir" || -L "$release_dir" ]]; then
  echo '该发布版本已存在，请使用新版本号' >&2
  exit 1
fi
previous=$(readlink -e -- "$app_dir/current" || true)
if [[ -n "$previous" && ( "${previous%/*}" != "$releases_dir" || ! -d "$previous" ) ]]; then
  echo '当前发布链接未指向有效版本目录' >&2
  exit 1
fi

for name in turtle-server turtle-admin turtle-ai-eval web.tar.gz; do
  if [[ ! -f "$source_dir/$name" || -L "$source_dir/$name" ]]; then
    echo "发布产物缺失或不是普通文件：$name" >&2
    exit 1
  fi
done

web_dir=$(mktemp -d -- "$source_dir/.web.XXXXXXXX")
stage=
ready=false

switch_release() {
  sudo ln -sfn -- "$1" "$app_dir/current.next" || return 1
  sudo mv -Tf -- "$app_dir/current.next" "$app_dir/current"
}

rollback_release() {
  if [[ -n "$previous" ]]; then
    if ! switch_release "$previous"; then
      echo '切回旧版本失败，请检查当前链接和服务状态' >&2
    elif ! sudo systemctl restart turtle-soup; then
      echo '旧版本链接已恢复，但服务未能重启，请检查服务状态' >&2
    else
      echo '新版本未完成发布验证，已切回旧版本' >&2
    fi
    return 0
  fi
  if sudo rm -- "$app_dir/current"; then
    echo '新版本未完成发布验证，已撤销首次发布链接' >&2
  else
    echo '首次发布链接未能撤销，请检查当前链接' >&2
  fi
  sudo systemctl stop turtle-soup || echo '停止服务失败，请检查服务状态' >&2
}

cleanup() {
  trap '' HUP INT TERM
  # 只有现用链接已切换且新版本尚未通过探活时才回退，包括发布中断。
  if [[ "$ready" == false && -L "$app_dir/current" && $(readlink -- "$app_dir/current") == "$release_dir" ]]; then
    rollback_release
  fi
  if [[ -n "$stage" && "${stage%/*}" == "$releases_dir" && "${stage##*/}" == .stage.* && -d "$stage" && ! -L "$stage" ]]; then
    sudo rm -rf -- "$stage" || echo '清理发布暂存目录失败' >&2
  fi
  if [[ -n "$web_dir" && "${web_dir%/*}" == "$source_dir" && "${web_dir##*/}" == .web.* && -d "$web_dir" && ! -L "$web_dir" ]]; then
    rm -rf -- "$web_dir" || echo '清理网页解包目录失败' >&2
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# 先检查归档路径，再以普通用户解包并检查符号链接。
if tar -tzf "$source_dir/web.tar.gz" | grep -E '(^/|(^|/)\.\.(/|$))' >/dev/null; then
  echo '网页归档包含非法路径' >&2
  exit 1
fi
tar -xzf "$source_dir/web.tar.gz" -C "$web_dir"
if [[ -n $(find "$web_dir" -type l -print -quit) ]]; then
  echo '网页归档包含符号链接' >&2
  exit 1
fi
test -f "$web_dir/index.html"

# 完整产物在同一文件系统内暂存，准备完才提交到正式版本目录。
stage=$(sudo mktemp -d -- "$releases_dir/.stage.XXXXXXXX")
sudo install -d -m 0755 "$stage/web"
for name in turtle-server turtle-admin turtle-ai-eval; do
  sudo install -m 0755 "$source_dir/$name" "$stage/$name"
done
sudo cp -a "$web_dir/." "$stage/web/"
sudo chown -R root:root "$stage"
sudo chmod -R go-w "$stage"
sudo chmod 0755 "$stage" "$stage/web"
sudo mv -nT -- "$stage" "$release_dir"
# mv -n 遇到同名目标可能返回成功，但会保留源目录。
if [[ -e "$stage" || -L "$stage" ]]; then
  echo '该发布版本已存在，请使用新版本号' >&2
  exit 1
fi
stage=
switch_release "$release_dir"

if sudo systemctl restart turtle-soup; then
  for attempt in {1..20}; do
    if sudo systemctl is-active --quiet turtle-soup && \
       curl --fail --silent --max-time 3 http://127.0.0.1:18080/health >/dev/null && \
       curl --fail --silent --max-time 3 http://127.0.0.1:18084/ >/dev/null; then
      ready=true
      break
    fi
    sleep 1
  done
fi
[[ "$ready" == true ]] || exit 1

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
