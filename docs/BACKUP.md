# 数据库备份到 Cloudflare R2

`scripts/backup.sh` 每天把 PostgreSQL 完整导出一份，连同 `.env` 和
`docker-compose.yml`，用口令加密后上传到 R2，并按保留天数清理旧备份。
主机上不需要额外安装任何东西：pg_dump 和 openssl 在数据库容器里跑，上传用
rclone 的容器镜像。

## 一次性准备

1. Cloudflare 控制台 → R2 → 创建一个存储桶，例如 `dreamtrans-backups`。
2. R2 → Manage R2 API Tokens → 创建令牌，权限选 **Object Read & Write**，
   只授权这个桶。记下 Access Key ID 和 Secret Access Key。
3. R2 概览页右侧能看到 **Account ID**。
安装脚本已经在 `~/dreamtrans/.env` 末尾留了一段注释掉的备份配置（旧安装
跑一次 `--update` 也会补上）。把前四行的 `#` 去掉并填上值：

```dotenv
R2_ACCOUNT_ID=你的账户 ID
R2_ACCESS_KEY_ID=...
R2_SECRET_ACCESS_KEY=...
R2_BUCKET=dreamtrans-backups
```

`BACKUP_PASSPHRASE` 不用填，下一步自动生成。`BACKUP_RETENTION_DAYS` 默认 30
天；`BACKUP_HEALTHCHECK_URL` 可选，填 healthchecks.io 之类的地址后，漏跑或
失败时监控会发邮件。

然后再跑一次更新：

```bash
curl -fsSL https://raw.githubusercontent.com/CoYumeLabs/DreamTrans/main/scripts/install.sh | bash -s -- --update --dir ~/dreamtrans
```

更新流程检测到 R2 配置后会自动：把最新的 `backup.sh` 放进安装目录、生成一个
40 位随机口令写进 `.env` 并在屏幕上打印一次、装好每天 03:15 的定时任务。
**把打印出来的口令立刻存到这台服务器以外的地方（密码管理器）。** `.env` 里
那份会随服务器一起丢失，没有口令，备份文件无法解开。

不想等定时任务，可以立刻手动跑一次确认链路通了：

```bash
~/dreamtrans/backup.sh --dry-run
~/dreamtrans/backup.sh
~/dreamtrans/backup.sh --list
```

看到 `backup complete` 且 `--list` 列出两个文件即可。之后每天 03:15（服务器
时间）执行，日志在 `~/dreamtrans/backups/backup.log`。本地保留最近 7 份，远端
按 `BACKUP_RETENTION_DAYS` 保留。`crontab -l` 能看到定时任务；手动管理可用
`~/dreamtrans/backup.sh --install-cron`。

## 恢复

在一台装好 Docker 的机器上，先按安装文档拉起一个空实例（或者就在原机上），
然后：

```bash
cd ~/dreamtrans
# 1. 从 R2 取回文件（也可以直接在 Cloudflare 控制台下载）
docker run --rm -e RCLONE_CONFIG_R2_TYPE=s3 -e RCLONE_CONFIG_R2_PROVIDER=Cloudflare \
  -e RCLONE_CONFIG_R2_ACCESS_KEY_ID=$R2_ACCESS_KEY_ID -e RCLONE_CONFIG_R2_SECRET_ACCESS_KEY=$R2_SECRET_ACCESS_KEY \
  -e RCLONE_CONFIG_R2_ENDPOINT=https://$R2_ACCOUNT_ID.r2.cloudflarestorage.com \
  -v "$PWD/restore:/restore" rclone/rclone:1.68 copy r2:$R2_BUCKET/dreamtrans /restore

# 2. 解密
export BACKUP_PASSPHRASE=你的口令
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass env:BACKUP_PASSPHRASE \
  -in restore/dreamtrans-20260906-031500.dump.enc -out restore/db.dump
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass env:BACKUP_PASSPHRASE \
  -in restore/dreamtrans-20260906-031500.config.tar.enc | tar -xf - -C restore/

# 3. 停应用，清空并重建数据库，导入
docker compose stop app
docker compose exec -T db psql -U dreamtrans -d postgres -c 'DROP DATABASE dreamtrans;' -c 'CREATE DATABASE dreamtrans;'
docker compose exec -T db pg_restore -U dreamtrans -d dreamtrans --no-owner < restore/db.dump
docker compose start app
```

如果主机上没有 openssl，把解密那两步换成
`docker compose exec -T db openssl ...` 并用 `-in /dev/stdin` 从管道读入即可。

## 检查清单

- 第一次跑完后，务必在另一台机器上做一次完整恢复演练，确认口令和流程都对。
- `backup.log` 里连续出现 ERROR 时 cron 不会通知你；配置
  `BACKUP_HEALTHCHECK_URL` 让监控在漏跑或失败时发邮件。
- 更换 `BACKUP_PASSPHRASE` 后，旧备份仍需旧口令才能解开，请把新旧口令都保存好。
