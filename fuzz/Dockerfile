FROM golang:1.16-buster as builder

RUN apt-get update && apt-get install -y git vim clang
RUN git clone https://github.com/fluxcd/kustomize-controller /kustomize-controller

WORKDIR /kustomize-controller

# fillippo.io/age v1.0.0-beta7 throws an error
RUN sed 's/filippo.io\/age v1.0.0-beta7/filippo.io\/age v1.0.0/g' -i /kustomize-controller/go.mod
RUN make download-crd-deps
RUN mkdir /kustomize-controller/fuzz 
COPY fuzz.go /kustomize-controller/controllers/

RUN go mod tidy

RUN cd / \
	&&  go get -u github.com/dvyukov/go-fuzz/go-fuzz@latest github.com/dvyukov/go-fuzz/go-fuzz-build@latest
RUN go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
RUN go get github.com/AdaLogics/go-fuzz-headers
RUN go mod download golang.org/x/sync
RUN go mod download github.com/dvyukov/go-fuzz

RUN mkdir /fuzzers
RUN cd /kustomize-controller/controllers \
	&& go-fuzz-build -libfuzzer -func=Fuzz \
	&& clang -o /fuzzers/Fuzz reflect-fuzz.a \
	-fsanitize=fuzzer

RUN cd /kustomize-controller/controllers && /fuzzers/Fuzz
