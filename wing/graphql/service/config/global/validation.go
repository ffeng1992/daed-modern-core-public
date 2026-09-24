package global

import "fmt"

// Validate before narrowing GraphQL signed integers into unsigned core fields.
func (i *Input) Validate() error {
	if i == nil {
		return nil
	}
	for name, value := range map[string]*int32{"tproxyPort": i.TproxyPort, "pprofPort": i.PprofPort} {
		if value != nil && (*value < 0 || *value > 65535) {
			return fmt.Errorf("%s must be between 0 and 65535", name)
		}
	}
	for name, value := range map[string]*int32{"soMarkFromDae": i.SoMarkFromDae, "bpfConnStateMapSize": i.BpfConnStateMapSize} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	return nil
}
