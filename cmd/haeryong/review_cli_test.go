package main

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	client "haeryong.local/haeryong/internal/adapters/client"
)

var _ = It("Review CLI maps actions and rejects missing conflicting or unsupported controls", Label("unit"), func() {
	for _, action := range []string{"start", "complete", "reopen", "clear"} {
		opt, err := parse([]string{"review", "set", "--version", "11111111-1111-4111-8111-111111111111", "--action", action, "--if-none-match", "*"})
		Expect(err).NotTo(HaveOccurred())
		Expect(validateCommandFlags(opt)).To(Succeed())
		path, conditions, err := reviewCLIInput(opt)
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(HaveSuffix("/review"))
		Expect(conditions).To(Equal(map[string]string{"If-None-Match": "*"}))
		Expect(reviewActionState(action)).NotTo(BeEmpty())
	}
	for _, args := range [][]string{
		{"review", "set", "--version", "11111111-1111-4111-8111-111111111111", "--action", "complete"},
		{"review", "set", "--version", "11111111-1111-4111-8111-111111111111", "--action", "scroll", "--if-none-match", "*"},
		{"review", "show", "--version", "11111111-1111-4111-8111-111111111111", "--if-match", "opaque"},
		{"review", "history", "--version", "11111111-1111-4111-8111-111111111111", "--limit", "0"},
	} {
		opt, err := parse(args)
		Expect(err).NotTo(HaveOccurred())
		Expect(validateCommandFlags(opt)).To(HaveOccurred())
	}
})

var _ = It("C1 Review CLI classifies reconciling receipts as exit six", Label("unit"), func() {
	for _, row := range []struct {
		status int
		code   string
		exit   int
	}{
		{409, "OPERATION_RECONCILIATION_REQUIRED", 6},
		{409, "OPERATION_CONFLICT", 4},
		{503, "SERVICE_RECOVERING", 5},
	} {
		var command commandError
		Expect(errors.As(productError(&client.Error{Status: row.status, Code: row.code}), &command)).To(BeTrue())
		Expect(command.code).To(Equal(row.exit))
	}
})
