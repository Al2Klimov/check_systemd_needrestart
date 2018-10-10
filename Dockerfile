FROM golang:1.11 as build

RUN go get github.com/golang/dep \
	&& go install github.com/golang/dep/...

ADD . /go/src/github.com/Al2Klimov/check_systemd_needrestart

RUN cd /go/src/github.com/Al2Klimov/check_systemd_needrestart \
	&& /go/bin/dep ensure \
	&& go generate \
	&& go install . \
	&& go build -o /go/bin/systemctl _docker/systemctl.go

FROM grandmaster/check-plugins-demo

COPY --from=build /go/bin/check_systemd_needrestart /usr/lib/nagios/plugins/
COPY --from=build /go/bin/systemctl /bin/
COPY icinga2/check_systemd_needrestart.conf _docker/icinga2-monobjs.conf /etc/icinga2/conf.d/
