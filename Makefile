TEST_PKGS=$(shell go list ./... | grep -v /vendor/ | grep -v /io | grep -v /connmatch )

test:
	go test -count 1 -p 1 -v -race $(TEST_PKGS)
compile:
	go build ./...
