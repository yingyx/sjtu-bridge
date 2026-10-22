package contracts

func jcourseMetadata(op, title string, aliases []string, description string) Capability {
	c := Capability{ID: JCoursePrefix + op, ContractVersion: "1", Title: title, Category: "learning", CategoryLabel: "学习与教学", TaskGroup: "curriculum", TaskGroupLabel: "培养方案与选课", Service: "jCourse 课程社区", Aliases: aliases, Keywords: []string{"jCourse", "选课社区", "课程评价", op}, Description: description, DataClass: PersonalData, ExecutionRule: GrantExecution, Effects: []Effect{NoEffect}, Reversibility: NotApplicable, Operation: Read, Delivery: QueryDelivery, Credentials: []string{"JCourseAPIKey"}, Provider: "jcourse"}
	if op == "enrolled" || op == "followed" || op == "ignored" {
		c.TaskGroup, c.TaskGroupLabel = "courses", "日常课程学习"
		c.CrossListings = []TaskPath{{"learning", "curriculum"}}
	}
	return c
}
