package deploy

type EventType string

const (
	EventRunStarted      EventType = "run_started"
	EventRunCompleted    EventType = "run_completed"
	EventServerStarted   EventType = "server_started"
	EventServerCompleted EventType = "server_completed"
	EventModuleStarted   EventType = "module_started"
	EventModuleSkipped   EventType = "module_skipped"
	EventModuleCompleted EventType = "module_completed"
)

type Event struct {
	Type EventType

	Server string
	Module string

	ServerIndex int
	ServerTotal int

	ServerModuleIndex int
	ServerModuleTotal int

	CompletedModules int
	TotalModules     int
}

type Observer interface {
	OnDeployEvent(Event)
}
