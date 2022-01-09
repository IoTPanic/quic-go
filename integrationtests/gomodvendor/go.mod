module test

go 1.15

// The version doesn't matter here, as we're replacing it with the currently checked out code anyway.
require github.com/IoTPanic/quic-go v0.21.0

replace github.com/IoTPanic/quic-go => ../../
