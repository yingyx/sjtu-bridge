package servicecatalog

import "github.com/yingyx/sjtu-bridge/contracts"

// campusAIClients links the school's independent model-client workflow. Public
// Bridge statistics require no key; model inference is not a Bridge read.
func campusAIClients() []contracts.ExternalClientGuide {
	return []contracts.ExternalClientGuide{{
		ID: "campus-ai-client", Title: "校园 AI 模型 API 与客户端配置",
		InstallURL: "https://claw.sjtu.edu.cn/guide/api-agent/", DocumentationURL: "https://claw.sjtu.edu.cn/guide/sjtu-api/",
		Setup: []string{
			"按学校客户端指南选择应用和安装入口；本次不安装或设置客户端。",
			"基础地址 https://models.sjtu.edu.cn/api/v1；学校列出 Chat Completions 的 /api/v1/chat/completions 和 Anthropic 格式 /api/v1/messages，按客户端章节选择模式和地址层级。",
			"Key 按学校文档在交我办主动申请；model 从本人获准 GET /api/v1/models 返回的 id 或客户端获取模型功能选择，不固定示例模型。",
		},
		Authentication: "上游 API Key 独立于 Bridge；经客户端专用密钥字段/受保护存储提供，不写命令参数、持久环境或日志。校园网条件以学校现行说明为准。",
		Scope:          "Bridge 公开状态/总体用量不需要 Key；外部模型目录读取需要上游凭据，推理/Agent 会消耗配额且不受 Bridge grant、撤销或审计约束。",
		Verification:   "2026-10-23 核实学校 API 与按客户端配置指南；未申请 Key、读取认证模型目录、调用模型或配置客户端。",
		Limitations:    []string{"学校宣称的协议兼容不代表所有原厂接口和客户端功能已适配；具体应用按学校当前章节核对。", "模型、额度、到期日期与资格随上游变化；停用时在上游撤销 Key，Bridge 撤销不代替它。"},
	}}
}

// externalExpansionRecords contains public locators and independently operated
// client guidance. It does not register capabilities or grant upstream access.
// Existing task IDs are enriched by the caller rather than published twice.
func externalExpansionRecords() []record {
	return []record{
		{entry: contracts.ServiceEntry{
			ID: "notes", Title: "水源文档", Service: "水源文档",
			Purpose:  "查找协作 Markdown 文档入口、网页导出和 HedgeDoc 1 外部工具说明。",
			Audience: "有权读取相应文档的用户", Owner: "水源文档维护方",
			Primary: path("digital", "communication"), VerifiedAt: "2026-10-23",
			Aliases: []string{"HedgeDoc", "HedgeDoc 1", "协作 Markdown"}, Keywords: []string{"导出", "Markdown", "外部CLI"},
			Destinations: []contracts.ServiceDestination{
				{Title: "水源文档", URL: "https://notes.sjtu.edu.cn/", Kind: "portal"},
				{Title: "水源文档功能与使用说明", URL: "https://notes.sjtu.edu.cn/features", Kind: "official_directory"},
			},
			Sources: []contracts.NavigationSource{
				{Title: "水源文档公开功能页", URL: "https://notes.sjtu.edu.cn/features"},
				{Title: "公开配置：HedgeDoc 1.12.0 版本标记", URL: "https://notes.sjtu.edu.cn/config"},
				{Title: "HedgeDoc 1 API 文档", URL: "https://docs.hedgedoc.org/dev/api/"},
			},
			ManualLimitations: []string{"私人文档与 jAccount 命令行认证未验；使用网页人工导出。"},
			ExternalClients: []contracts.ExternalClientGuide{{
				ID: "hedgedoc-cli", Title: "HedgeDoc 1 官方 Bash CLI（可选公开文档导出）",
				InstallURL: "https://github.com/hedgedoc/cli", DocumentationURL: "https://docs.hedgedoc.org/dev/api/",
				Setup:          []string{"偶尔导出优先使用网页菜单；已有 WSL/Bash 且具备 curl、wget、jq、Git 时再选择官方脚本。", "在个人工具目录克隆官方仓库并审阅、选定版本；设置当前终端 HEDGEDOC_SERVER=https://notes.sjtu.edu.cn，运行 bin/hedgedoc 查看帮助。", "只使用 export --md 导出用户指定且可匿名读取的已知文档；不要用 import 测试连接或 export --html（会发布文档）。"},
				Authentication: "仅提供无需凭据的公开文档路径；校园 jAccount CLI 登录未验证。",
				Scope:          "独立外部工具；导出读取正文并写本地文件，导入、编辑和发布会修改上游。",
				Verification:   "2026-10-23 匿名公开 /config 报告 HedgeDoc 1.12.0；未实际安装或执行 CLI。",
				Limitations:    []string{"Bash 脚本不能直接按原用法在 Windows PowerShell 中运行；启动会创建本地配置目录，也可能迁移旧配置。", "Markdown 导出缺少完整 HTTP 错误/超时处理，必须核验内容；不复用浏览器 Cookie，不套用 HedgeDoc 2 API/token。"},
			}},
		}},
		{entry: contracts.ServiceEntry{
			ID: "latex", Title: "SJTU LaTeX 文档助手", Service: "SJTU LaTeX",
			Purpose:  "查找学校 Overleaf 协作入口、项目源文件导出与条件式 Git 工具指引。",
			Audience: "有权访问相应项目的用户", Owner: "SJTU LaTeX 维护方",
			Primary: path("digital", "software"), VerifiedAt: "2026-10-23",
			Aliases: []string{"Overleaf", "LaTeX 协作平台"}, Keywords: []string{"TeX", "项目导出", "Git集成"},
			Destinations: []contracts.ServiceDestination{{Title: "SJTU LaTeX 文档助手", URL: "https://latex.sjtu.edu.cn/", Kind: "portal"}},
			Sources: []contracts.NavigationSource{
				{Title: "学校登录页：Overleaf 品牌与 jAccount 入口", URL: "https://latex.sjtu.edu.cn/"},
				{Title: "Overleaf 自建实例 Git 集成（Server Pro）", URL: "https://docs.overleaf.com/on-premises/configuration/overleaf-toolkit/server-pro-only-configuration/git-integration"},
			},
			ManualLimitations: []string{"学校 CE/Pro 版本、Git/API 开关及私人项目未验证；编译和编辑不属于 Bridge 只读查询。"},
			ExternalClients: []contracts.ExternalClientGuide{{
				ID: "overleaf-local-workflow", Title: "网页导出与本地 Git（仅学校开通 Git 后）",
				InstallURL: "https://git-scm.com/install/windows", DocumentationURL: "https://docs.overleaf.com/on-premises/configuration/overleaf-toolkit/server-pro-only-configuration/git-integration",
				Setup:          []string{"先在网页下载有权访问项目的源文件 ZIP，在个人目录使用已有编辑器与 TeX 工具链。", "只有学校项目菜单提供 Git 克隆地址与认证入口时，才安装 Git 并按界面地址克隆；未显示则继续网页导出。"},
				Authentication: "网页使用学校 jAccount；条件式 Git 路径按学校项目界面提供的独立令牌认证。",
				Scope:          "本地工具独立运行；克隆/拉取读取远端并写本地文件，推送修改远端项目。",
				Verification:   "2026-10-23 仅核学校登录页 Overleaf 品牌；Git 与 API 可用性尚未核实。",
				Limitations:    []string{"上游自建 Git 集成仅适用于启用该功能的 Server Pro，不能推定学校已开通。", "不提供虚构的学校 Overleaf CLI，不使用要求复制浏览器会话的同步脚本。"},
			}},
		}},
		{entry: contracts.ServiceEntry{
			ID: "gitlab", Title: "SJTU Git 源代码管理", Service: "SJTU Git",
			Purpose:  "查找校园代码托管、Git 读取与可选 glab 外部工具配置说明。",
			Audience: "具有学校账号和相应项目权限的用户", Owner: "SJTU Git 维护方",
			Primary: path("digital", "software"), VerifiedAt: "2026-10-23",
			Aliases: []string{"极狐GitLab", "GitLab", "代码托管"}, Keywords: []string{"Git", "glab", "外部CLI", "仓库"},
			Destinations: []contracts.ServiceDestination{
				{Title: "SJTU Git", URL: "https://git.sjtu.edu.cn/sjtu/in.html", Kind: "portal"},
				{Title: "SJTU Git 帮助", URL: "https://git.sjtu.edu.cn/sjtu/help.html", Kind: "official_directory"},
			},
			Sources: []contracts.NavigationSource{
				{Title: "学校帮助：基于极狐 GitLab 旗舰版构建", URL: "https://git.sjtu.edu.cn/sjtu/help.html"},
				{Title: "GitLab CLI 官方项目与兼容要求", URL: "https://gitlab.com/gitlab-org/cli/"},
			},
			ManualLimitations: []string{"学校精确版本、API、PAT 策略与私人项目未实测。"},
			ExternalClients: []contracts.ExternalClientGuide{
				{
					ID: "git", Title: "Git 仓库读取", InstallURL: "https://git-scm.com/install/windows", DocumentationURL: "https://docs.gitlab.com/user/profile/personal_access_tokens/",
					Setup:          []string{"安装 Git for Windows 并重开 PowerShell，用 git --version 检查。", "从学校项目 Clone 菜单复制 HTTPS 地址，在个人目录克隆；不在 URL 中嵌入凭据。"},
					Authentication: "学校允许 PAT 时，HTTPS 仓库读取优先 read_repository 范围，凭据经交互或受保护的凭据管理器提供。",
					Scope:          "Git 直接连接上游；clone/fetch 读取远端并写本地，push 修改远端。",
					Verification:   "2026-10-23 核实学校公开入口与帮助；没有实际克隆项目或创建凭据。",
					Limitations:    []string{"具体认证与角色以学校当前项目设置为准；SSH 密钥可能同时具有写权限。"},
				},
				{
					ID: "glab", Title: "GitLab CLI（glab，可选）", InstallURL: "https://gitlab.com/gitlab-org/cli/-/releases", DocumentationURL: "https://docs.gitlab.com/cli/authentication/",
					Setup:          []string{"Windows 从官方 Releases 安装匹配架构的二进制，或执行 winget install --id GLab.GLab --exact --source winget；重开终端后检查 glab version。", "需要议题、合并请求等命令时再运行 glab auth login --hostname git.sjtu.edu.cn，选择 Token 交互认证。"},
					Authentication: "官方完整 PAT 工作流要求 api 与 write_repository，具有写权限；使用受保护存储，不将令牌放在 --token 参数或明文配置。",
					Scope:          "独立外部工具；创建议题、合并、推送、重跑流水线与发布均可能改变上游，不受 Bridge grant 约束。",
					Verification:   "glab 上游支持 GitLab 16.0+；学校极狐版本、接口与此客户端兼容性未实测。",
					Limitations:    []string{"仅需读仓库时选择 Git 的 read_repository 路径；不把完整 glab PAT 当作只读凭据。", "学校 OAuth 应用注册未核，不默认 --web；本地 logout 不代替上游撤销令牌。"},
				},
			},
		}},
		{entry: contracts.ServiceEntry{
			ID: "software-mirrors", Title: "SJTUG 软件源镜像与配置", Service: "SJTUG 镜像",
			Purpose:  "定位 SJTUG 镜像、按软件源查配置帮助并为已有包管理器选择下载来源。",
			Audience: "需要使用开源软件镜像的用户", Owner: "上海交通大学 Linux 用户组（SJTUG）",
			Primary: path("digital", "software"), VerifiedAt: "2026-10-23",
			Aliases: []string{"SJTUG mirrors", "思源镜像", "致远镜像", "FTP软件源"}, Keywords: []string{"软件源", "npm", "镜像源", "包管理器"},
			Destinations: []contracts.ServiceDestination{
				{Title: "SJTUG 软件源镜像服务", URL: "https://mirrors.sjtug.sjtu.edu.cn/", Kind: "portal"},
				{Title: "软件源配置帮助", URL: "https://mirror.sjtu.edu.cn/docs/", Kind: "official_directory"},
				{Title: "镜像站新闻与维护说明", URL: "https://mirrors.sjtug.sjtu.edu.cn/news", Kind: "official_directory"},
			},
			Sources: []contracts.NavigationSource{
				{Title: "SJTUG 镜像服务与服务器分工", URL: "https://mirrors.sjtug.sjtu.edu.cn/"},
				{Title: "维护方 npm-registry 配置帮助", URL: "https://mirror.sjtu.edu.cn/docs/npm-registry"},
			},
			ManualLimitations: []string{"镜像同步与下载未实测；配置和安装由用户在已有工具中独立选择。"},
			ExternalClients: []contracts.ExternalClientGuide{{
				ID: "npm-mirror", Title: "已有 npm 的单次镜像配置示例", InstallURL: "https://docs.npmjs.com/downloading-and-installing-node-js-and-npm", DocumentationURL: "https://mirror.sjtu.edu.cn/docs/npm-registry",
				Setup:          []string{"已有 npm 时运行 npm --version；无需安装通用 SJTUG CLI。", "单次查询公共包：npm view npm version --registry=https://mirrors.sjtug.sjtu.edu.cn/npm-registry。", "去掉 --registry 即恢复原有来源；其他软件源分别按各自帮助配置。"},
				Authentication: "公共镜像无需校园登录；不发送私人 registry token。",
				Scope:          "例子只读取公共包元数据，npm 可能写本地缓存/日志；安装和持久修改源属于另外的本机变更。",
				Verification:   "2026-10-23 核实现行 npm-registry 帮助与 npm view 官方用法，未执行下载或修改配置。",
				Limitations:    []string{"不同软件源地址和配置不能互换；保持 TLS 校验，长期改源先保存原配置与回退方法。"},
			}},
		}},
	}
}
