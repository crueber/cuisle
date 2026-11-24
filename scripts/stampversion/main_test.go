package main

import (
	"encoding/json"
	"testing"
)

func TestStamp_SetsEveryVersionField(t *testing.T) {
	in := []byte(`{"fixed":{"file_version":"0.0.1","product_version":"0.0.1"},"info":{"0409":{"FileVersion":"0.0.1","ProductVersion":"0.0.1","ProductName":"Cuisle"}}}`)
	out, err := stamp(in, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Fixed map[string]string            `json:"fixed"`
		Info  map[string]map[string]string `json:"info"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	if d.Fixed["file_version"] != "1.2.3" || d.Fixed["product_version"] != "1.2.3" ||
		d.Info["0409"]["FileVersion"] != "1.2.3" || d.Info["0409"]["ProductVersion"] != "1.2.3" || d.Info["0409"]["ProductName"] != "Cuisle" {
		t.Fatalf("not stamped: %s", out)
	}
}

func TestRun_RejectsANonNumericVersion(t *testing.T) {
	if err := run("v1.2.3", "x", "y"); err == nil {
		t.Fatal("accepted v1.2.3")
	}
}
