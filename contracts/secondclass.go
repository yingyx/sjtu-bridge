package contracts

func IsSecondClassCapability(id string) bool {
	return id == SecondClassHonorsCapability || id == SecondClassPracticeCapability || id == SecondClassEducationCapability
}

func SecondClassModules(id string) []string {
	switch id {
	case SecondClassHonorsCapability:
		return []string{"scholarship", "innovation", "volunteering_award", "social_practice_award", "arts_award", "outstanding_graduate", "student_leader"}
	case SecondClassPracticeCapability:
		return []string{"volunteering", "social_practice"}
	case SecondClassEducationCapability:
		return []string{"labor", "lecture", "cultural_activity"}
	default:
		return nil
	}
}

func ValidSecondClassModule(id, module string) bool {
	for _, allowed := range SecondClassModules(id) {
		if module == allowed {
			return true
		}
	}
	return false
}

func addSecondClassOutput(id string, props map[string]any, required *[]string, str map[string]any) {
	props["module"] = map[string]any{"type": "string", "enum": SecondClassModules(id)}
	*required = append(*required, "module")
	for _, key := range []string{"started_at", "ended_at", "data_source"} {
		props[key] = str
	}
	if id == SecondClassHonorsCapability {
		for _, key := range []string{"organization", "awarded_at", "level", "role"} {
			props[key] = str
		}
	} else {
		props["duration"] = map[string]any{"type": "string", "maxLength": 64, "pattern": `^[0-9]+(\.[0-9]+)?$`}
		props["duration_unit"] = map[string]any{"type": "string", "maxLength": 64}
		if id == SecondClassPracticeCapability {
			props["role"] = str
			props["level"] = map[string]any{"type": "string", "maxLength": 2048, "description": "社会实践源类型，仅 social_practice 模块使用"}
		} else if id == SecondClassEducationCapability {
			props["level"] = map[string]any{"type": "string", "maxLength": 2048, "description": "劳动教育源类型，仅 labor 模块使用"}
		}
	}
}
