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
`GMS_GAME_SERVER_ADDRESS`、`GMS_GAME_SERVER_API_KEY`、`GMS_PORTAL_API_BASE`。

官网模块相关：`databases.portal` 官网库 `cow-portal`（不配则 `/portal/*` 整组不挂载）；`portal.apiBase` 官网后端公开接口
（拉邀请积分口径，缺省 `https://cow-portal-backend.cowgalaxy.com`）、`portal.imageHosting` 图床前缀（解析头像 / 牛牛图）、
`portal.siteUrl` 官网站点（拼邀请链接）。官网库与游戏库在同一 MySQL 实例时，官网用户列表会直接跨库 JOIN 游戏账号。
`portal.social` 为三个官方渠道的直连凭证，与官网 `configs/system.toml [social.*]` 保持一致：`telegram.botToken/chatId`（bot 需在群内）、
`x.target/bearerToken`（X Developer Portal → Keys and tokens → Bearer Token，app-only；不配则 X 只看站内数据）、`discord.botToken/guildId`。
也可用环境变量 `GMS_PORTAL_TG_BOT_TOKEN` `GMS_PORTAL_TG_CHAT_ID` `GMS_PORTAL_X_BEARER_TOKEN` `GMS_PORTAL_DC_BOT_TOKEN` `GMS_PORTAL_DC_GUILD_ID` 覆盖。

容器：`docker compose up -d --build`（`Dockerfile` 多阶段构建，镜像内含 `config/`）。
线上只保留 `gms-ranch.cowgalaxy.com` 一个域名反代到本服务（Java 时代的 `gms-auth` / `gms-taskrunner` 已下线）；
前端默认用 `https://gms-ranch.cowgalaxy.com`，也可通过 `VITE_APP_API_BASE` 指定。

## 目录

```
cmd/gms/main.go              组合根:连库、挂路由、启动调度与 HTTP
internal/config              JSON 配置 + 环境变量覆盖
internal/httpx               错误码/JSON 响应/参数解析/CORS 等中间件
internal/auth                密码(sha1+盐)、令牌、权限树、会话缓存与鉴权中间件
internal/perm                功能清单与权限码(feature/{key}/view|edit、game/ranch/{region})
internal/query               只读列表助手:列白名单 → 过滤/排序/分页/CSV 导出
internal/modules/authcenter  登录、个人中心、管理员、角色、多语言、枚举元数据
internal/modules/gms         邮件/群邮件/签到/公会战配置的增删改、审核、激活、同步;游戏服签名与代理
internal/modules/ranchdata   玩家、公会、公会字典、公会战记录、斗牛场日志(只读 + 导出)
internal/modules/analysis    da_ranch_* 统计表(只读 + 导出)
internal/modules/dashboard   概览页核心指标:游戏规模与活跃 / 官网资产与增长 / 近 30 天趋势 / 待办与任务健康,四库汇总,60 秒缓存
internal/modules/task        cron 调度、执行日志、手动补跑、三个牧场统计任务
internal/modules/portalinvite 官网公测邀请计划的管理与报表(读 cow-portal 库 u_invite_*);
                             临时挂在 GMS,只依赖 httpx/auth/perm/query,库连接 / 权限码 / 路由自成一组,
                             将来连同前端 src/modules/portal-invite 一起迁到官网管理后端
internal/modules/portaluser  官网用户管理:以钱包地址为主键,汇总官网库 / 游戏库 / 日志库里的全部画像
                             (资料、邀请码与积分、社交绑定、公测领取、牛牛、星球、市场、游戏账号与任务、登录 IP),
                             可为用户预分配邀请码;与 portalinvite 同属官网业务,一起迁移
internal/modules/portalsocial 官网社交媒体运营看板:X / Telegram / Discord 的站内社交任务漏斗 / 留存 / 取关 /
                             反作弊 / 积分 / 账号质量 / 同 IP / 复查健康度,以及直连平台 API 的官方渠道规模
                             (粉丝 / 群成员)与每日快照(u_social_channel_stat);凭证在 config portal.social
internal/portalapi           官网后端公开接口的小客户端:拉 /invite/rules(等级门槛 / 分值)并缓存 10 分钟,
                             官网改口径后 GMS 报表自动跟上;迁到官网后可删
```

## 接口

与前端的约定沿用原版：鉴权头 `x-api-token`；成功返回裸 JSON；失败返回非 2xx + `{"code","message"}`，
令牌失效为 `403 {"code":101}`；分页 `page`(0 起)/`size`，返回 `{content,totalElements}`；排序 `sort=列,asc|desc`。

| 分组 | 路径 |
|---|---|
| 登录 | `POST /login`（body `{username,password,otp?}`；账号开启二步验证而未带 `otp` 返回 `{"code":206}`，验证码错误 `207`；账号没有本部署区域的权限返回 `403 {"code":100}`）`POST /login-with-token` `POST /logout` |
| 个人中心 | `POST /me/change-password` `/me/update-profile` `/me/update-settings`（form）；二步验证：`POST /me/2fa/setup`（返回 `{secret,uri}` 出二维码，10 分钟内确认）`POST /me/2fa/enable`（form `code`）`POST /me/2fa/disable`（form `code`，只认验证码） |
| 权限 | `GET /permission/tree` `GET /permission/features`（功能清单，角色编辑器的「查看 / 可更改」矩阵） |
| 概览 | `GET /dashboard/summary`（`?refresh=1` 跳过 60 秒缓存；登录即可，但按权限裁剪：没有游戏 / 官网 / 任务等相应「查看」权限的板块置空或归零）|
| 管理员 | `GET /system/users/options`（仅 uid / 用户名 / 昵称，登录即可，供表格翻译创建人 / 审核人）；`GET /system/users`（完整列表，需 `system-user/view`）`POST /system/users` `PUT/DELETE /system/users/{uid}` `POST /system/users/{username}/lock\|unlock\|update_role\|reset_2fa`（写操作需 `system-user/edit`，且受下文「授权边界」约束） |
| 角色 | `GET /system/roles`（需 `system-user/view`）`POST /system/roles` `PUT/DELETE /system/roles/{role}`（需 `system-user/edit` + 授权边界） |
| 多语言 | `GET /locale/language` `/languages`（只有 `zhCN` `enUS`）`/query?lang=` `POST /locale/language` `PUT /locale/language/{langKey}`（upsert）`DELETE /locale/language/{langKey}` |
| 枚举 | `GET /meta/enum` `GET /meta/enum/{code}`（表格枚举列翻译用，登录即可）；写接口仍在但前端已不再提供编辑入口 |
| 区域 / 模板 | `GET /config/info` `GET /template/item` |
| 运营配置 | `{m}` ∈ `mail` `group-mail` `check-in` `guild-battle`：`GET/POST /{m}` `GET/PUT/DELETE /{m}/{id}` `GET /{m}/export` `POST /{m}/sync` `POST /{m}/approval?ids=` `POST /{m}/reject?ids=` `POST /{m}/{id}/approval\|reject\|enable\|disable` |
| 游戏服代理 | 只放行白名单：`GET /proxy/admin/config/queryAllConfig`（`game-config/view`）`POST /proxy/admin/config/saveConfig`（`game-config/edit`），其它路径一律 403 |
| 游戏数据 | `GET /ranch/users[/{uid}\|/export]` `/ranch/guilds[/{guildId}\|/export]` `/ranch/guild-dict` `/ranch/guild-battles` `/ranch/bullring-logs` |
| 统计 | `GET /analysis/bullring-count` `/bullring-rewards` `/user` `/retention`（均有 `/export`） |
| 任务 | `GET /task/list` `GET /task/log?className=` `POST /task/enableOrDisableTask` `/task/updateCronTrigger` `/task/manualSchedule` |
| 官网邀请计划（需配置 `databases.portal`） | 口径：`GET /portal/invite/rules`（代理官网 `/invite/rules`）；报表：`GET /portal/invite/overview` `/trend?days=` `/leaderboard?limit=` `/ips?min=`；列表（含 `/export`）：`GET /portal/invite/users` `/point-logs` `/admin-logs`；详情：`GET /portal/invite/users/{address或邀请码}`；管理：`POST /portal/invite/users/{address}/flag\|unflag\|adjust`（body `{reason, points}`）。权限 `feature/portal-invite/view`（含导出）/ `edit`（flag / unflag / adjust） |
| 官网社交媒体（需配置 `databases.portal`） | 站内：`GET /portal/social/overview`（三平台漏斗 / 状态 / 积分 / 今日 / 复查健康度）`/trend?days=` `/quality`（X 粉丝数与账号年龄分布）`/ips?min=`；账号列表（含 `/export`）：`GET /portal/social/accounts`（不返回 token 列）；官方渠道：`GET /portal/social/channels`（直连 X / Telegram / Discord，10 分钟缓存，`?refresh=1` 需 manage）`/channels/history?days=`（每日快照）`POST /portal/social/channels/snapshot`。权限 `feature/portal-social/view`（含导出）/ `edit`（refresh / snapshot）。进程启动 30 秒后及之后每小时检查当天快照，缺则自动补 |
| 官网用户管理（需配置 `databases.portal`） | 列表：`GET /portal/users`（基表 `u_profile` LEFT JOIN 邀请 / 星球 / 游戏账号，支持 `address` `name_like` `invite_code` `inviter` `planet_id` `social_like` `game_uid` `game_guild` `last_login_ip` 等过滤与排序，`/export` 导出）；概览：`GET /portal/users/stats`；详情：`GET /portal/users/{地址或邀请码或游戏UID}`（三库全量画像，分节容错）；管理：`POST /portal/users/{address}/invite-code`（预分配邀请码，已有则原样返回）。权限 `feature/portal-user/view`（含导出）/ `edit`（预分配邀请码）。游戏字段只在官网库与游戏库同实例时直接 JOIN（`GameSchemaForPortal`），否则列表不含游戏列、详情仍分库查询 |

列表过滤参数（只对代码里声明可过滤的列生效）：`col=v`（重复即 IN）、`col_like=`、`col_from=`/`col_to=`、`col_gt=`/`col_lt=`/`col_ne=`。
导出为同步返回的 UTF-8（带 BOM）CSV，时间按 `timezone` 参数格式化，枚举列按枚举元数据翻译。

## 权限模型

以「功能」为单位，每个功能只有两档，角色勾选时一目了然：

| 权限码 | 含义 |
|---|---|
| `feature/{key}/view` | 只读：列表 / 详情 / 报表、导出 |
| `feature/{key}/edit` | 可更改：新建 / 修改 / 删除 / 审核 / 同步 / 启停 / 标记 / 调分 等一切写操作（前端勾可更改时自动含查看） |
| `game/ranch/{region}` | 区域可见性（登录后可切换的区域） |

功能 key（`internal/perm.Features`，也是 `GET /permission/features` 的返回）：`ranch-user` `ranch-guild`（纯查询）、`mail` `group-mail` `game-config`（签到 + 游戏服参数 / 代理）`guild-battle`、`analysis`（全部报表，纯查询）、`portal-user` `portal-social` `portal-invite`、`task`、`system-setting`（多语言）、`system-user`（用户与角色）。
根通配 `{"code":"","wildcard":true}` 为超级管理员；`feature` 节点通配 = 全部功能可更改（含未来新增）；`game/ranch` 通配 = 全部区域。
`/dashboard/summary`（按权限裁剪）`/config/info` `/template/item` `/system/users/options` `/meta/enum`(GET) `/locale/language/query|languages` 登录即可。
旧的 `service/*` `table/*` `function/*` 码已废弃，角色迁移脚本见 `cow-startup/SQL2.0/migrations/2026-10-10_gms_feature_permissions.sql`（内置 `ADMIN` 超管、`ALL` 全功能可更改、`VIEWER` 全功能只读）；内置 `admin` 账号挂 `ADMIN`（`2026-10-10_gms_admin_superuser.sql`）。

**区域是后端硬约束。** 登录时账号必须拥有本部署的 `game/ranch/{regionCode}`，否则直接拒绝；已登录的请求由全局中间件 `auth.RequireScope` 再校验一次（`/logout` `/me/*` 豁免）。dev / prod 共用授权库、token 通用，所以不能只靠前端藏区域。

**授权边界（`Tree.Covers`）：不能给出自己没有的东西。** 持有 `system-user/edit` 的人：
- 新建 / 修改角色时，新权限树必须 ⊆ 自己的权限；修改 / 删除已有角色时，该角色原有权限也必须 ⊆ 自己的（防止低权限者削掉高权限角色）；`ADMIN` 角色只有超级管理员能改。
- 新建用户、改角色、改资料 / 重置密码、锁定、重置 2FA、删除：目标账号现有权限必须 ⊆ 自己的，分配的新角色也必须 ⊆ 自己的。
- 不能改自己的角色、不能锁定 / 删除自己；内置 `admin` / `root` 账号不可删、角色不可改。
因此只有超级管理员能创建另一个超级管理员；`ALL` 能管一切功能，但管不了 `ADMIN` 角色和 `ADMIN` 账号。

## 二步验证

TOTP（RFC 6238，SHA1 / 6 位 / 30 秒，±1 步容差），与 Google / Microsoft Authenticator、1Password 等兼容，密钥存 `user.twoStepSecret`（空 = 未开启）。
绑定流程：`/me/2fa/setup` 生成待确认密钥（仅内存，10 分钟）→ 用户扫码后用当前验证码调 `/me/2fa/enable` 才落库。开启后 `/login` 必须带 `otp`。
关闭：本人 `/me/2fa/disable` 只认当前验证码（密码不能替代，否则 token 被盗 + 密码泄露即可拆掉第二道锁）；丢失认证器由权限覆盖该账号的管理员 `POST /system/users/{username}/reset_2fa`。

## 与 Java 版的行为差异

- 删除运营配置为软删除（`deletedTime`），列表与同步均不再包含；已激活的记录不允许再修改（游戏服只在首次同步时插入，改了也不会生效）。
- 签到奖励等附件的代币数量仍由游戏服同步时 ×10^18，这里只做 JSON 合法性校验。
- 登出会清掉库里的 token；角色或用户权限改动后会话缓存即时失效，无需等 24 小时。
- 定时任务的"日"按 `task.timeZone`（默认 Asia/Shanghai）切分；任务名沿用 Java 类全名，历史 `task_status` / `task_log` 直接可用。
- 原 `/table/*`、`/config/ranchconfig`、`/token/query-session`、`/permission/sync`、`/meta/enum/sync` 等服务间接口不再提供。
