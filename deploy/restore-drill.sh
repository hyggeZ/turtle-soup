#!/usr/bin/env bash
# 在 mine 上把受信的 turtle_soup 逻辑备份恢复到私有 PostgreSQL 实例。
set -euo pipefail
umask 077

fail() { printf '恢复演练失败：%s\n' "$1" >&2; exit 1; }

as_pg() {
  sudo -n -u postgres env -u PGHOSTADDR -u PGSERVICE -u PGSERVICEFILE -u PGOPTIONS \
    LC_ALL=C TZ=UTC "$@"
}

now_ms() { awk '{printf "%.0f\n", $1 * 1000}' /proc/uptime; }

if [[ $# == 2 && $1 == --archive && $2 == /* ]]; then
  mode=archive
  input=$(as_pg realpath -e -- "$2" 2>/dev/null || sudo -n realpath -e -- "$2" 2>/dev/null) ||
    fail '备份路径无法解析'
  [[ $input =~ ^/srv/turtle-soup/backups/[[:alnum:]_.-]+\.dump$ ]] || fail '备份须位于项目的私有 backups 目录'
elif [[ $# == 1 && $1 == --fresh ]]; then
  mode=fresh
else
  fail '用法：restore-drill.sh --archive /srv/turtle-soup/backups/name.dump 或 --fresh'
fi

pg_bin=$(pg_config --bindir)
[[ $pg_bin = /* ]] || fail 'PostgreSQL 工具目录无效'
for binary in initdb pg_ctl pg_restore pg_dump psql createdb; do
  [[ -x $pg_bin/$binary ]] || fail "缺少 PostgreSQL 工具 $binary"
done

drill_start=$(now_ms)
drill_start_utc=$(date -u +%FT%TZ)
work=$(as_pg mktemp -d /var/tmp/turtle-drill-167.XXXXXXXX)
[[ $work =~ ^/var/tmp/turtle-drill-167\.[[:alnum:]]{8}$ ]] || fail '临时目录名无效'
data_dir=$work/data
socket_dir=$work/socket
archive=$work/snapshot.dump
db=turtle_167_drill
port=55432

# 先停掉本次实例，再删除经过校验的私有临时目录。
cleanup() {
  local code=$?
  trap - EXIT
  if [[ $work =~ ^/var/tmp/turtle-drill-167\.[[:alnum:]]{8}$ ]] &&
     [[ $(as_pg realpath -e -- "$work" 2>/dev/null || true) == "$work" ]] &&
     [[ $(as_pg stat -c %a -- "$work" 2>/dev/null || true) == 700 ]]; then
    if as_pg test -f "$data_dir/postmaster.pid"; then
      as_pg "$pg_bin/pg_ctl" -D "$data_dir" -w -t 15 -m fast stop >/dev/null 2>&1 ||
        fail '临时实例停止失败，保留临时目录以便检查'
    fi
    as_pg rm -rf -- "$work" || fail '清理本次临时目录失败'
  else
    fail '临时目录身份变化，停止清理'
  fi
  exit "$code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
[[ $(as_pg stat -c %a -- "$work") == 700 ]] || fail '临时目录权限无效'
as_pg mkdir -m 700 -- "$socket_dir"

# 临时快照只停留在此私有目录。归档模式以 0600 复制指定存档。
if [[ $mode == fresh ]]; then
  as_pg install -m 0600 /dev/null "$archive"
  dump_start=$(now_ms)
  snapshot_epoch=$(date +%s)
  as_pg env -u TZ PGOPTIONS='-c default_transaction_read_only=on' timeout 30s "$pg_bin/pg_dump" --host=/var/run/postgresql --port=5432 \
    --username=postgres --no-password --dbname=turtle_soup --format=custom --lock-wait-timeout=5s \
    --file="$archive" >/dev/null 2>&1 || fail '生成只读快照失败'
  printf 'fresh_dump_elapsed_ms=%s\n' "$(( $(now_ms) - dump_start ))"
elif as_pg test -f "$input" && as_pg test -r "$input"; then
  as_pg install -m 0600 -- "$input" "$archive" || fail '无法读取指定存档'
else
  sudo -n test -f "$input" || fail '指定路径不是可读取的存档文件'
  sudo -n install -o postgres -g "$(id -gn postgres)" -m 0600 -- "$input" "$archive" ||
    fail '无法复制受保护存档到本次私有目录'
fi
as_pg test -r "$archive" && [[ $(as_pg stat -c '%U:%a' -- "$archive") == postgres:600 ]] ||
  fail '临时备份未获得预期的所属用户或权限'

header_value() {
  awk -v label="$1" '$0 ~ ("^;[[:space:]]+" label ":?[[:space:]]+") {
    sub("^;[[:space:]]+" label ":?[[:space:]]+", ""); print; exit
  }' <<< "$archive_list"
}

# Custom archive 记录的是创建时的墙上时间，此脚本运行于生成存档的服务器。
archive_list=$(as_pg env -u TZ "$pg_bin/pg_restore" --list "$archive" 2>/dev/null) || fail '无法读取存档目录'
[[ $(header_value Format) == CUSTOM && $(header_value dbname) == turtle_soup ]] ||
  fail '存档必须是 turtle_soup 的 PostgreSQL custom dump'
archive_created=$(header_value 'Archive created at')
[[ -n $archive_created ]] || fail '存档缺少创建时间'
archive_epoch=$(env -u TZ LC_ALL=C date -d "${archive_created% *}" +%s 2>/dev/null) || fail '存档创建时间无法解析'
if [[ $mode == archive ]]; then
  snapshot_epoch=$archive_epoch
fi
printf 'source_mode=%s\narchive_created_utc=%s\narchive_bytes=%s\nsource_pg=%s\npg_dump_version=%s\n' \
  "$mode" "$(date -u -d "@$snapshot_epoch" +%FT%TZ)" "$(as_pg stat -c %s -- "$archive")" \
  "$(header_value 'Dumped from database version')" "$(header_value 'Dumped by pg_dump version')"

target=(--host="$socket_dir" --port="$port" --username=postgres --no-password)
ready_start=$(now_ms)
as_pg "$pg_bin/initdb" --pgdata="$data_dir" --auth-local=peer --auth-host=scram-sha-256 \
  --encoding=UTF8 --locale=C >/dev/null 2>&1 || fail '创建私有 PostgreSQL 实例失败'
as_pg "$pg_bin/pg_ctl" -D "$data_dir" -l "$work/postgresql.log" -w -t 15 \
  -o "-p $port -c listen_addresses='' -c unix_socket_directories=$socket_dir -c unix_socket_permissions=0700" \
  start >/dev/null 2>&1 || fail '启动私有 PostgreSQL 实例失败'
as_pg "$pg_bin/createdb" "${target[@]}" "$db" 2>/dev/null || fail '创建空的演练数据库失败'

restore_start=$(now_ms)
as_pg timeout 120s "$pg_bin/pg_restore" "${target[@]}" --dbname="$db" --exit-on-error \
  --single-transaction --no-owner --no-privileges "$archive" >/dev/null 2>&1 || fail '恢复存档失败'
restore_elapsed=$(( $(now_ms) - restore_start ))

# PostgreSQL 只返回校验标志、行数和版本，不输出业务明细。
result=$(as_pg timeout 60s "$pg_bin/psql" "${target[@]}" --dbname="$db" -XqAt -F '|' -v ON_ERROR_STOP=1 2>/dev/null <<'SQL'
BEGIN READ ONLY;
SET LOCAL search_path TO public;
SELECT 'meta','target_pg',current_setting('server_version');

SELECT 'check','migrations_contiguous', count(*) > 0 AND min(version) = 1 AND max(version) = count(*)
FROM schema_migrations;
SELECT 'meta','schema_version',max(version) FROM schema_migrations;

WITH core(name) AS (VALUES ('users'),('sessions'),('soups'),('rooms'),('room_members'),
                          ('room_events'),('ai_games'),('ai_events'),('ai_attempts'))
SELECT 'check','core_tables',bool_and(to_regclass(format('public.%I',name)) IS NOT NULL) FROM core;

WITH required(child, column_name, parent) AS (
  VALUES ('sessions','user_id','users'), ('soups','author_id','users'),
         ('rooms','owner_id','users'), ('rooms','host_id','users'), ('rooms','soup_id','soups'),
         ('room_members','room_id','rooms'), ('room_members','user_id','users'),
         ('room_events','room_id','rooms'), ('room_events','user_id','users'),
         ('ai_games','user_id','users'), ('ai_games','soup_id','soups'),
         ('ai_events','game_id','ai_games'), ('ai_attempts','game_id','ai_games')
)
SELECT 'check','foreign_keys',bool_and(EXISTS (
  SELECT 1 FROM pg_constraint c
  JOIN pg_attribute a ON a.attrelid=c.conrelid AND c.conkey=ARRAY[a.attnum]
  WHERE c.contype='f' AND c.convalidated AND a.attname=r.column_name
  AND c.conrelid=to_regclass(format('public.%I',r.child))
  AND c.confrelid=to_regclass(format('public.%I',r.parent))
)) FROM required r;

SELECT 'metric','users',count(*) FROM users
UNION ALL SELECT 'metric','soups',count(*) FROM soups
UNION ALL SELECT 'metric','rooms',count(*) FROM rooms
UNION ALL SELECT 'metric','room_events',count(*) FROM room_events
UNION ALL SELECT 'metric','ai_games',count(*) FROM ai_games
UNION ALL SELECT 'metric','ai_events',count(*) FROM ai_events;

SELECT 'check','core_join_counts',
  (SELECT count(*) FROM rooms r JOIN soups s ON s.id=r.soup_id) = (SELECT count(*) FROM rooms)
  AND (SELECT count(*) FROM room_events e JOIN rooms r ON r.id=e.room_id) = (SELECT count(*) FROM room_events)
  AND (SELECT count(*) FROM ai_events e JOIN ai_games g ON g.id=e.game_id) = (SELECT count(*) FROM ai_events);

-- 等价于匿名题目首屏的筛选、关联作者与排序。旧 schema 缺少的可见性字段视为空。
WITH visible AS (
  SELECT s.id,s.author_id,s.created_at FROM soups s
  WHERE to_jsonb(s)->>'deleted_at' IS NULL AND to_jsonb(s)->>'hidden_at' IS NULL
), first_page AS (
  SELECT s.id FROM visible s JOIN users u ON u.id=s.author_id
  ORDER BY s.created_at DESC,s.id DESC LIMIT 20
)
SELECT 'check','public_soup_list', (SELECT count(*) FROM first_page) = LEAST(20,(SELECT count(*) FROM visible));
COMMIT;
SQL
) || fail '恢复库的验收查询执行失败'

printf '%s\n' "$result"
check_count=$(awk -F '|' '$1=="check" {++n} END {print n+0}' <<< "$result")
pass_count=$(awk -F '|' '$1=="check" && $3=="t" {++n} END {print n+0}' <<< "$result")
printf 'checks_passed=%s/%s\nrestore_elapsed_ms=%s\nempty_instance_to_verified_ms=%s\ndrill_started_utc=%s\n' \
  "$pass_count" "$check_count" "$restore_elapsed" "$(( $(now_ms) - ready_start ))" "$drill_start_utc"
completed_epoch=$(date -u +%s)
printf 'drill_completed_utc=%s\ndrill_elapsed_ms=%s\narchive_age_seconds=%s\n' \
  "$(date -u -d "@$completed_epoch" +%FT%TZ)" "$(( $(now_ms) - drill_start ))" "$(( completed_epoch - snapshot_epoch ))"
[[ $check_count == 5 && $pass_count == 5 ]] || fail '验收检查未全部通过'
