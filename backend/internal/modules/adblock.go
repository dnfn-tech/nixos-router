package modules

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

const MaxEntries = 4096

func stringArray(value interface{}) ([]string, bool) {
	if value == nil {
		return nil, true
	}
	if values, ok := value.([]string); ok {
		return values, true
	}
	values, ok := value.([]interface{})
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(values))
	for _, v := range values {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		result = append(result, s)
	}
	return result, true
}

func RenderDNS(values map[string]interface{}) (string, error) {
	for key := range values {
		if key != "entries" && key != "lists" {
			return "", invalid("adblock", "config", "unknown configuration field")
		}
	}
	lists, ok := stringArray(values["lists"])
	if !ok || len(lists) > 0 {
		return "", invalid("adblock", "config.lists", "subscription downloads are not supported")
	}
	entries, ok := stringArray(values["entries"])
	if !ok || len(entries) > MaxEntries {
		return "", invalid("adblock", "config.entries", "expected at most 4096 domain names")
	}
	seen := map[string]bool{}
	for i, entry := range entries {
		d := strings.TrimSuffix(strings.ToLower(entry), ".")
		valid := len(d) > 0 && len(d) <= 253 && net.ParseIP(d) == nil
		for _, label := range strings.Split(d, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				valid = false
				break
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					valid = false
				}
			}
		}
		if !valid {
			return "", invalid("adblock", fmt.Sprintf("config.entries.%d", i), "expected an ASCII DNS domain name")
		}
		seen[d] = true
	}
	domains := make([]string, 0, len(seen))
	for d := range seen {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	var b strings.Builder
	for _, d := range domains {
		// local= prevents forwarding non-address queries. # returns null IPv4 and
		// IPv6 addresses and applies to this domain and its subdomains.
		fmt.Fprintf(&b, "local=/%s/\naddress=/%s/#\n", d, d)
	}
	return b.String(), nil
}
