//go:build !darwin || !cgo

package macos

type unavailableInputBatchDriver struct{}

func newHostInputBatchDriver() inputBatchDriver { return unavailableInputBatchDriver{} }

func (unavailableInputBatchDriver) prepareBatch(inputBatchOperation) (preparedInputBatch, error) {
	return nil, errMouseBrokerUnavailable
}
