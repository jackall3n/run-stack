//go:build darwin && cgo

package dns

/*
#include <dns_sd.h>
#include <stdlib.h>
#include <sys/select.h>

static void ignore(DNSServiceRef service, DNSRecordRef record, DNSServiceFlags flags, DNSServiceErrorType code, void *context) {}

static DNSServiceErrorType add(DNSServiceRef connection, DNSRecordRef *record, const char *name, uint16_t kind, uint16_t size, const void *data) {
	// Shared, not unique: the name is ours alone, and shared records are live at once instead of
	// after a second of probing the network for a conflict.
	return DNSServiceRegisterRecord(connection, record, kDNSServiceFlagsShared, 0, name, kind, kDNSServiceClass_IN, size, data, 120, ignore, NULL);
}

// Reads whatever replies the responder has sent, without waiting for more.
static void drain(DNSServiceRef connection) {
	int fd = DNSServiceRefSockFD(connection);

	for (;;) {
		fd_set ready;
		struct timeval now = {0, 0};

		FD_ZERO(&ready);
		FD_SET(fd, &ready);

		if (select(fd + 1, &ready, NULL, NULL, &now) <= 0 || DNSServiceProcessResult(connection) != kDNSServiceErr_NoError) {
			return;
		}
	}
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

const supported = true

var (
	loopback4 = [4]byte{127, 0, 0, 1}
	loopback6 = [16]byte{15: 1}
)

// responder registers address records with mDNSResponder over one connection. The records
// disappear when the connection closes, so a proxy that dies takes its names with it.
type responder struct {
	mu         sync.Mutex
	connection C.DNSServiceRef
	records    map[string][2]C.DNSRecordRef
}

// New returns an announcer backed by the system's mDNS responder.
func New() Announcer {
	announcer := &responder{records: map[string][2]C.DNSRecordRef{}}

	if !announcer.connect() {
		return nothing{}
	}

	return announcer
}

func (announcer *responder) connect() bool {
	announcer.records = map[string][2]C.DNSRecordRef{}

	return C.DNSServiceCreateConnection(&announcer.connection) == C.kDNSServiceErr_NoError
}

// register adds both address records for a host. A lookup that asks for IPv4 and IPv6 together
// fails now and then when only one of them exists.
func (announcer *responder) register(host string) bool {
	name := C.CString(host)
	defer C.free(unsafe.Pointer(name))

	var records [2]C.DNSRecordRef

	if C.add(announcer.connection, &records[0], name, C.kDNSServiceType_A, 4, unsafe.Pointer(&loopback4[0])) != C.kDNSServiceErr_NoError {
		return false
	}

	if C.add(announcer.connection, &records[1], name, C.kDNSServiceType_AAAA, 16, unsafe.Pointer(&loopback6[0])) != C.kDNSServiceErr_NoError {
		return false
	}

	announcer.records[host] = records

	return true
}

func (announcer *responder) Set(hosts []string) {
	announcer.mu.Lock()
	defer announcer.mu.Unlock()

	wanted := local(hosts)

	C.drain(announcer.connection)

	for host, records := range announcer.records {
		if !wanted[host] {
			C.DNSServiceRemoveRecord(announcer.connection, records[0], 0)
			C.DNSServiceRemoveRecord(announcer.connection, records[1], 0)
			delete(announcer.records, host)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		failed := false

		for host := range wanted {
			if _, done := announcer.records[host]; !done && !announcer.register(host) {
				failed = true

				break
			}
		}

		if !failed {
			return
		}

		// The responder was restarted and the connection went with it: start again from nothing.
		C.DNSServiceRefDeallocate(announcer.connection)

		if !announcer.connect() {
			return
		}
	}
}

func (announcer *responder) Close() {
	announcer.mu.Lock()
	defer announcer.mu.Unlock()

	C.DNSServiceRefDeallocate(announcer.connection)
	announcer.records = map[string][2]C.DNSRecordRef{}
}
