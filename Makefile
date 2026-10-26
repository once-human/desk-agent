.PHONY: build test vet fmt extension install gui-test e2e
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/desk-agent .
test:
	go test -count=1 ./...
vet:
	go vet ./...
fmt:
	gofmt -l -w .
extension:
	extension/build.sh
install:
	./install.sh
gui-test: build
	python3 -W ignore -m unittest discover -s gui/tests -p 'test_*.py'
	GSK_RENDERER=cairo xvfb-run -a -s "-screen 0 1400x1500x24" python3 gui/tests/shots.py /tmp/desk-agent-shots --check
e2e: build
	python3 test/e2e/agent_smoke.py
	python3 test/e2e/hooks_smoke.py
	python3 test/e2e/live_reload_smoke.py
