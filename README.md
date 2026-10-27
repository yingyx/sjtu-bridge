# SJTU Bridge

![SJTU Bridge — 一处部署，多端共用；任务导航，直达能力；凭据托管，最小授权。](assets/hero.svg)

把上海交通大学校园服务接入命令行、应用和 AI 助手。公开信息直接查询，个人数据按应用授权，权限可随时撤销。

项目定位覆盖校园服务的读取与受控写入；当前支持查询及云盘非覆盖新建；可信审批和高影响写入尚未开放。以 Windows 为主要开发与验证平台。

## 核心亮点

- **一处部署，多端共用**：本机安装一份 Bridge，第三方应用与 Agent 通过 CLI、MCP 或 Client SDK 接入，共享校园服务适配与个人服务的单实例 Broker，无需各自维护门户对接与认证逻辑。
- **任务导航，直达能力**：在统一事项目录中查用途、适用对象和官方入口，再调用已获准的结构化能力。Canvas、IDS、第二课堂和校历保留按任务组织的命令；分散事项通过目录定位官网办理，同一事项也可同时提供两种方式。
- **凭据托管，最小授权**：受支持的上游凭据留在本机 Broker，应用通过一个连接持有多项独立授权，按需限定能力、账号、课程与返回视图，并可逐项修改或撤销。减少向第三方应用分发高权限凭据带来的越权风险。

当前个人服务支持 Canvas Token、jCourse 用户 API Key 和按门户独立绑定的 JAAuthCookie；教务课表、邮箱标题、校园活动、第二课堂成长记录、本人宿舍电量与洗浴信息已在 Windows 上通过限定账号与资源的真实只读验收。应用按具体资源与视图授权，由 Broker 统一处理撤销；其他账号与平台兼容性仍需验证。

## 项目定位

校园服务分散在不同门户，入口、数据格式和认证方式各不相同。查找一项服务、为应用接入数据，往往需要重复寻找入口、适配接口并处理凭据。

SJTU Bridge 提供统一的访问层，让用户通过命令行查询信息，让开发者通过稳定的能力契约接入服务，也让 Agent 通过 MCP 使用同一套能力。个人服务的凭据由本机 Broker 托管，应用只获得指定账号、课程和字段范围内的权限。

当前接入包括：

- **公开数据查询**：校历与教务公告、借阅规则、食堂采样及今日/昨日时段曲线、医院与团委公告归档和正文、收费公示；IDS 学期节次、教室筛选与设施、单日课表、未排课教室、课程检索、建筑楼层、通知、办公联系及失物索引；软件、超算硬件、网络支持和机构目录。各项覆盖范围见能力描述，无需登录。
- **个人只读查询**：Canvas 获准课程、公告、学习模块、页面、作业要求与本人提交反馈、文件及附件、录播讲次与已有文字；教务课表、成绩与考试、课堂考勤、校园卡余额与近期流水；邮箱文件夹、搜索、正文与小附件；云盘目录、元数据与文件内容；水源主题、帖子与通知；活动筛选、日历、本人报名和收藏；第二课堂成长记录；本人宿舍电量与洗浴信息；jCourse 课程、教师、点评及本人列表。另有个人日历、交我办消息分组与待办、视频会议及AI转写查询。能力按账号、资源与字段分别授权；发现输出说明实际范围，空列表验收不代表非空详情已验证。
- **受限写入**：在明确获准的云盘父目录中新建目录，或上传至多32KiB的新文件；禁止覆盖，不自动重试。目录创建与23字节新文件上传已通过单账号验收，并只读下载核对完整文件内容。会议变更、AI转写上传与推理尚未开放执行。
- **官方服务导航**：定位第二课堂、宿舍水电与洗浴人工办理、软件授权、场馆预约及缴费等业务入口，明确查询范围和接入状态。

## 快速开始

准备 [go.mod](go.mod) 指定版本的 Go，在 Windows PowerShell 的仓库根目录构建：

```powershell
. .\scripts\dev-env.ps1
.\scripts\build.ps1
```

公开查询无需登录或启动后台进程：

```powershell
.\bin\sjtu.exe services search 成绩
.\bin\sjtu.exe services describe failed-grade-record --json
.\bin\sjtu.exe capabilities search 校历
.\bin\sjtu.exe capabilities describe public.calendars.list
.\bin\sjtu.exe learning calendars list --json
.\bin\sjtu.exe library borrowing list --json
.\bin\sjtu.exe digital software list --json
.\bin\sjtu.exe learning classrooms types --json
.\bin\sjtu.exe life dining series --canteen-id 100 --day both --limit 100 --json
```

使用 `services list/search/describe` 查找已发布的校园事项、官方入口和当前可用能力；事项使用独立语义ID，不授予权限，也不代表已接入数据。使用 `capabilities list/search` 发现全部已接入能力及所需凭据和授权，无需登录或启动 Broker；`capabilities describe 能力ID` 查看参数和配置要求。带 `--connection` 时查看该应用当前获准能力及具体范围；实际调用仍校验权限。`--verbose` 查看完整契约。业务命令统一按领域组织，例如 `sjtu learning canvas assignments list`；CLI/MCP 共享同一套能力映射。

CLI 默认提供便于阅读的文本；Agent 使用 `--json` 获取紧凑 JSON，业务结果保留完整来源、时间、分页、范围和警告。追加 `--verbose` 展开详细说明，或为 JSON 增加缩进。公共与个人业务查询均支持 `--input-file` / `--input-json-stdin`，具体参数与限额可通过 `--help` 和能力详情查看。

## 接入应用与 Agent

支持 CLI、MCP stdio 和 Go Client SDK。MCP 宿主可用以下命令接入公开能力：

```powershell
.\bin\sjtu.exe mcp serve
```

个人服务先使用 `broker init` 初始化，再以 `broker run` 保持服务运行；在另一终端先用 `auth import` 导入，再用 `apps setup 应用名 --output 连接文件绝对路径` 选择已有账号、能力与范围并交付连接。对已有应用再次运行 `apps setup 应用名` 可调整授权；原子操作仍可使用 `apps create` 和 `permissions grant`。Canvas Token、jCourse API Key 或 JAAuthCookie 在终端隐藏输入；应用通过独立连接文件访问，不取得上游凭据。

教务和邮箱默认可运行 `auth login`，使用交我办扫码，或复用 Broker 已保存的门户凭据；活动、第二课堂成绩单、电费和洗浴通过 `--providers activity,dekt,electric,bath` 显式选择。各门户分别核验并确认账号后导入，无需手填 Cookie 或 UA。

配置个人能力的 MCP 宿主时，使用 `mcp serve --connection 应用连接文件绝对路径`。连接文件需保存在私有目录，宿主配置只引用路径。用 `permissions list --app 应用名` 查看授权，`permissions revoke --grant GRANT_ID` 撤销其中一项，或用 `apps revoke --app 应用名` 撤销整个应用。

通用内容通道提供 `content describe/download`、Go SDK 分块下载和 MCP 内容资源。它只消费能力签发的短期引用；Canvas课件与本人提交附件、可用的录播直链媒体、邮箱小附件和云盘文件由独立能力签发引用；HLS录播合成尚不支持。引用绑定应用、资源及版本，每块最多256KiB；跨语言接入可参考[纯标准库 Python 示例](examples/python/bridge_client.py)。

## 开发

项目使用 Go、Cobra 和官方 MCP Go SDK，采用模块化单体结构。本地 AGENTS.md 与 docs/ 保存开发约定、设计、操作和验证记录。

```powershell
go test ./...
go vet ./...
```

公共 Go API 位于 [bridge](bridge/)，连接 Broker 的 Client SDK 位于 [client](client/)，能力契约与目录位于 [contracts](contracts/)，校园服务适配位于 [internal/providers](internal/providers/)。新增能力须显式注册和授权，分类本身不授予权限。
