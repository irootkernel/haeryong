package main

import (
	"bytes"

	"encoding/json"

	"net/http"
	"os/exec"

	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

func authRequest(t GinkgoTInterface, base, method, path, token string, body []byte) (int, json.RawMessage) {
	GinkgoHelper()
	request, e := http.NewRequest(method, base+path, bytes.NewReader(body))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	if body != nil {
		request.Header.Set("Origin", base)
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, e := client.Do(request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	defer response.Body.Close()
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(response.Body).Decode(&out)
	return response.StatusCode, out.Data
}
func bootstrapOwnerCLI(t GinkgoTInterface, s service.Selection, base, label string) (domain.Session, string) {
	GinkgoHelper()
	body, _ := json.Marshal(map[string]string{"device_label": label})
	status, data := authRequest(t, base, "POST", "/api/v1/auth/pairings", "", body)
	Expect(status != 201).To(BeFalse(), func() string { return fmt.Sprintf("pair create HTTP %d", status) })

	var pairing domain.PairingCreated
	Expect(json.Unmarshal(data, &pairing) != nil).To(BeFalse(), func() string { return fmt.Sprint("pairing response") })

	cli(t, 0, append([]string{"auth", "pairings"}, profileArgs(s)...)...)
	op, _ := service.NewID()
	intent, _ := json.Marshal(map[string]string{"code": pairing.Code, "origin": pairing.Origin, "etag": pairing.ETag, "operation_id": op})
	command := exec.Command(testBinary, append([]string{"auth", "approve", pairing.ID}, profileArgs(s)...)...)
	command.Stdin = bytes.NewReader(intent)
	{
		_, e := command.Output()
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("protected exact bootstrap failed", e) })
	}

	body, _ = json.Marshal(map[string]string{"polling_secret": pairing.PollingSecret})
	status, data = authRequest(t, base, "POST", "/api/v1/auth/pairings/"+pairing.ID+"/poll", "", body)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("single redemption HTTP %d", status) })

	var poll domain.PairingPoll
	Expect(json.Unmarshal(data, &poll) != nil || poll.Session == nil || poll.Token == "").To(BeFalse(), func() string { return fmt.Sprint("single credential missing") })

	return *poll.Session, poll.Token
}
