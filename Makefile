CROSS_GOOS := darwin windows
VBASH_DIR  := testdata/vbash-bench
VBASH_TARGET ?= /bin/bash

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

vbash-check:
	@cd $(VBASH_DIR) && ./run.py --target $(VBASH_TARGET) --min-rates "T0=100,T1=100,T2=95,T3h=90,T3c=0,C=100,X=100"

vbash-record:
	@cd $(VBASH_DIR) && ./run.py --record

vbash-naive:
	@cd $(VBASH_DIR) && ./run.py --target ./tools/reference-naive.py

.PHONY: test cross vbash-check vbash-record vbash-naive
