package command

type Command string

const (
	CommandQuestion       Command = "question"
	CommandTimeInquiry    Command = "timeInquiry"
	CommandWeatherInquiry Command = "weatherInquiry"
)
