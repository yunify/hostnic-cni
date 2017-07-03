package provider

import "testing"

func TestProviderS(t *testing.T) {
	conf := make(map[string]interface{})
	_, err := New("qingcloud", conf)
	if err != nil {
		t.Error(err)
	}
}
