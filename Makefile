CROSS_GOOS := darwin windows

test:
	go test ./...

cross:
	@set -e; for os in $(CROSS_GOOS); do \
		echo "== GOOS=$$os go vet ./..."; \
		GOOS=$$os go vet ./...; \
		echo "== GOOS=$$os go test -c（逐包，仅编译不运行）"; \
		for p in $$(go list ./...); do \
			GOOS=$$os go test -c -o /dev/null $$p; \
		done; \
	done
	@echo "cross: OK"

.PHONY: test cross
