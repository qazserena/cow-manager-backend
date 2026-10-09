# 游戏管理平台（GMS）— Go 后端

原 Java 版（`../cow-manager-backend-java`，auth-center / gms-ranch / task-runner 三个 Spring Boot 服务，已废弃、仅供参考）的 Go 重写版，
**一个进程、一个端口**提供全部接口。不再实现通用 Table 框架（schema + 条件 DSL），每个资源都是普通 REST；
前端 `cow-manager-frontend` 已同步改为本地声明列、按普通 query 参数过滤。

- 语言 / 依赖：Go 1.25，仅 `go-sql-driver/mysql`、`sqlx`、`robfig/cron`
- 数据库：直连 `auth_center`、`gms-ranch`、`ranch_game`、`ranch_log`、`ranch_tpl` 五个库，**表结构不变**，既有账号、token、角色、任务记录全部沿用

## 运行

```bash
go build -o gms ./cmd/gms
./gms -config config/dev.json        # 或 GMS_CONFIG=... ./gms
go test ./...
```

配置见 `config/dev.json` / `config/prod.json`；可用环境变量覆盖：`GMS_LISTEN`、`GMS_DB_HOST`、`GMS_DB_PASSWORD`、
`GMS_GAME_SERVER_ADDRESS`、`GMS_GAME_SERVER_API_KEY`。

容器：`docker compose up -d --build`（`Dockerfile` 多阶段构建，镜像内含 `config/`）。
线上只保留 `gms-ranch.cowgalaxy.com` 一个域名反代到本服务（Java 时代的 `gms-auth` / `gms-taskrunner` 已下线）；
前端默认用 `https://gms-ranch.cowgalaxy.com`，也可通过 `VITE_APP_API_BASE` 指定。

## 目录

```
cmd/gms/main.go              组合根:连库、挂路由、启动调度与 HTTP
internal/config              JSON 配置 + 环境变量覆盖
internal/httpx               错误码/JSON 响应/参数解析/CORS 等中间件
internal/auth                密码(sha1+盐)、令牌、权限树、会话缓存与鉴权中间件
internal/perm                全部权限码及其中文名(构成 /permission/tree)
internal/query               只读列表助手:列白名单 → 过滤/排序/分页/CSV 导出
internal/modules/authcenter  登录、个人中心、管理员、角色、多语言、枚举元数据
internal/modules/gms         邮件/群邮件/签到/公会战配置的增删改、审核、激活、同步;游戏服签名与代理
internal/modules/ranchdata   玩家、公会、公会字典、公会战记录、斗牛场日志(只读 + 导出)
internal/modules/analysis    da_ranch_* 统计表(只读 + 导出)
internal/modules/task        cron 调度、执行日志、手动补跑、三个牧场统计任务
```

## 接口

与前端的约定沿用原版：鉴权头 `x-api-token`；成功返回裸 JSON；失败返回非 2xx + `{"code","message"}`，
令牌失效为 `403 {"code":101}`；分页 `page`(0 起)/`size`，返回 `{content,totalElements}`；排序 `sort=列,asc|desc`。

| 分组 | 路径 |
|---|---|
| 登录 | `POST /login` `POST /login-with-token` `POST /logout` |
| 个人中心 | `POST /me/change-password` `/me/update-profile` `/me/update-settings`（form） |
| 权限 | `GET /permission/tree` |
| 管理员 | `GET/POST /system/users` `PUT/DELETE /system/users/{uid}` `POST /system/users/{username}/lock\|unlock\|update_role` |
| 角色 | `GET/POST /system/roles` `PUT/DELETE /system/roles/{role}` |
| 多语言 | `GET /locale/language` `/languages` `/query?lang=` `POST /locale/language` `PUT/DELETE /locale/language/{langKey}` |
| 枚举 | `GET/POST /meta/enum` `PUT/DELETE /meta/enum/{code}` `GET/POST /meta/enum/{code}` `PUT/DELETE /meta/enum/{code}/{itemCode}` |
| 区域 / 模板 | `GET /config/info` `GET /template/item` |
| 运营配置 | `{m}` ∈ `mail` `group-mail` `check-in` `guild-battle`：`GET/POST /{m}` `GET/PUT/DELETE /{m}/{id}` `GET /{m}/export` `POST /{m}/sync` `POST /{m}/approval?ids=` `POST /{m}/reject?ids=` `POST /{m}/{id}/approval\|reject\|enable\|disable` |
| 游戏服代理 | `/proxy/admin/...`（自动签名转发到 login 服务） |
| 游戏数据 | `GET /ranch/users[/{uid}\|/export]` `/ranch/guilds[/{guildId}\|/export]` `/ranch/guild-dict` `/ranch/guild-battles` `/ranch/bullring-logs` |
| 统计 | `GET /analysis/bullring-count` `/bullring-rewards` `/user` `/retention`（均有 `/export`） |
| 任务 | `GET /task/list` `GET /task/log?className=` `POST /task/enableOrDisableTask` `/task/updateCronTrigger` `/task/manualSchedule` |

列表过滤参数（只对代码里声明可过滤的列生效）：`col=v`（重复即 IN）、`col_like=`、`col_from=`/`col_to=`、`col_gt=`/`col_lt=`/`col_ne=`。
导出为同步返回的 UTF-8（带 BOM）CSV，时间按 `timezone` 参数格式化，枚举列按枚举元数据翻译。

## 与 Java 版的行为差异

- 删除运营配置为软删除（`deletedTime`），列表与同步均不再包含；已激活的记录不允许再修改（游戏服只在首次同步时插入，改了也不会生效）。
- 签到奖励等附件的代币数量仍由游戏服同步时 ×10^18，这里只做 JSON 合法性校验。
- 登出会清掉库里的 token；角色或用户权限改动后会话缓存即时失效，无需等 24 小时。
- 定时任务的"日"按 `task.timeZone`（默认 Asia/Shanghai）切分；任务名沿用 Java 类全名，历史 `task_status` / `task_log` 直接可用。
- 原 `/table/*`、`/config/ranchconfig`、`/token/query-session`、`/permission/sync`、`/meta/enum/sync` 等服务间接口不再提供。
