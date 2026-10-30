package device

// BlockedSMSDestination reports whether the recipient is in a barred
// country. VoNet has no destination-country block, so valid numbers remain
// available to every send transport. The shared hook keeps the HTTP API,
// Telegram, automatic tasks and MCP on one policy if restrictions are added
// later without letting one transport diverge from the others.
func BlockedSMSDestination(phone string) (bool, string) {
	return false, ""
}
