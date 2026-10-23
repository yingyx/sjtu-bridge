package servicecatalog

import "github.com/yingyx/sjtu-bridge/contracts"

func aiExpansionRecords() []record {
	return []record{{entry: contracts.ServiceEntry{
		ID: "campus-ai", Title: "校内 AI 服务与模型 API", Service: "校内 AI 服务", Purpose: "按任务选择校园对话、AI应用、语音、模型API及致远一号公开状态与用量统计。", Audience: "校内师生；各平台、模型与API分别核验权限", Owner: "网络信息中心", Primary: path("digital", "software"), VerifiedAt: "2026-10-23",
		Aliases: []string{"致远一号", "AI应用", "模型API", "API Key", "大模型", "语音转录"}, Keywords: []string{"aic", "chat", "translate", "OpenAI兼容", "Anthropic", "Token统计"},
		SupportedCapabilityIDs: []string{contracts.AIStatusCapability, contracts.AIUsageCapability},
		Destinations: []contracts.ServiceDestination{
			{Title: "校园对话入口", URL: "https://chat.sjtu.edu.cn/", Kind: "portal", Purpose: "在网页使用校园模型；不是模型API授权。"},
			{Title: "交我办AI应用平台", URL: "https://my.sjtu.edu.cn/ai/", Kind: "official_directory", Purpose: "按用途定位AI应用，应用权限分别判断。"},
			{Title: "AI语音服务", URL: "https://translate.sjtu.edu.cn/ai/ui/transcribe", Kind: "portal", Purpose: "在官网核对转录服务及上传规则。"},
			{Title: "致远一号服务门户", URL: "https://aic.sjtu.edu.cn/", Kind: "portal", Purpose: "核对智算平台和本人服务入口。"},
			{Title: "公开模型统计与个人用量登录入口", URL: "https://aic.sjtu.edu.cn/top/", Kind: "public_statistics", Purpose: "公开模型状态及汇总可在Bridge读取；本人用量登录留在官网。", RelatedCapabilityIDs: []string{contracts.AIStatusCapability, contracts.AIUsageCapability}},
			{Title: "模型API申请与调用官方指南", URL: "https://claw.sjtu.edu.cn/guide/sjtu-api/", Kind: "official_documentation", Purpose: "按当前指南人工申请API；模型、额度、有效期及网络范围以官网为准。"},
			{Title: "模型API接入外部客户端", URL: "https://claw.sjtu.edu.cn/guide/api-agent/", Kind: "official_documentation", Purpose: "选择客户端及协议，独立配置模型服务，不通过Bridge代调用。"},
		},
		Sources:           []contracts.NavigationSource{{Title: "致远一号当前公开统计与前端调用", URL: "https://aic.sjtu.edu.cn/top/"}, {Title: "网信模型API官方指南", URL: "https://claw.sjtu.edu.cn/guide/sjtu-api/"}, {Title: "学校模型服务导航", URL: "https://claw.sjtu.edu.cn/"}},
		ExternalClients:   campusAIClients(),
		ManualLimitations: []string{"公开统计不包含个人与机构排行榜；接口读不到本人模型权限、配额或密钥。", "对话、语音上传、模型推理、密钥申请与管理均在官网或独立客户端处理；模型调用消耗额度，不作为Bridge普通只读查询。"},
	}}}
}
