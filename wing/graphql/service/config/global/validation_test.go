package global

import "testing"

func TestUnsignedInputCannotWrap(t *testing.T) {
	for _, v := range []int32{-1, 65536, 2147483647} {
		i := Input{TproxyPort: &v}
		if _, err := i.Marshal(); err == nil {
			t.Errorf("port %d silently accepted", v)
		}
	}
	v := int32(-1)
	for _, i := range []Input{{SoMarkFromDae: &v}, {BpfConnStateMapSize: &v}} {
		if _, err := i.Marshal(); err == nil {
			t.Error("negative unsigned field silently accepted")
		}
	}
}
