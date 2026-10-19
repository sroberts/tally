package remote

// Service is the remote listener's Bonjour service type.
const Service = "_tally._tcp"

// txt is the instance's TXT record: only the protocol version and host id.
func txt(hostID string) []string { return []string{"v=3", "id=" + hostID} }
