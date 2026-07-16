package bus

// FrontendEventRoute declares a public bus topic exposed through the desktop
// shell and the event name used by frontend subscribers.
type FrontendEventRoute struct {
	Topic     Topic
	EventName string
}

var frontendEventRoutes = [...]FrontendEventRoute{
	{Topic: TopicProviderChanged, EventName: "provider:changed"},
	{Topic: TopicDockerConnected, EventName: "docker:connected"},
	{Topic: TopicDockerReconnecting, EventName: "docker:reconnecting"},
	{Topic: TopicDockerDisconnected, EventName: "docker:disconnected"},
	{Topic: TopicObjectsChanged, EventName: "objects:changed"},
	{Topic: TopicProjectChanged, EventName: "project:changed"},
	{Topic: TopicProviderInstallProgress, EventName: "provider:install:progress"},
	{Topic: TopicImagePullProgress, EventName: "image:pull:progress"},
	{Topic: TopicImagePushProgress, EventName: "image:push:progress"},
	{Topic: TopicUpdatesCheckProgress, EventName: "updates:check:progress"},
	{Topic: TopicUpdatesApplied, EventName: "updates:applied"},
	{Topic: TopicLogsLines, EventName: "logs:lines"},
	{Topic: TopicLogsEOF, EventName: "logs:eof"},
	{Topic: TopicLogsError, EventName: "logs:error"},
	{Topic: TopicTerminalData, EventName: "terminal:data"},
	{Topic: TopicTerminalClosed, EventName: "terminal:closed"},
	{Topic: TopicStatsSample, EventName: "stats:sample"},
	{Topic: TopicJobProgress, EventName: "job:progress"},
	{Topic: TopicJobDone, EventName: "job:done"},
	{Topic: TopicNotification, EventName: "notification"},
	{Topic: TopicPortForwardChanged, EventName: "portforward:changed"},
}

// FrontendEventRoutes returns the complete desktop bus-to-frontend contract.
// A copy prevents callers from changing the process-wide catalog.
func FrontendEventRoutes() []FrontendEventRoute {
	routes := make([]FrontendEventRoute, len(frontendEventRoutes))
	copy(routes, frontendEventRoutes[:])
	return routes
}
