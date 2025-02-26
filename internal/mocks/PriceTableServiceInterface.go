// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import billing "github.com/paysuper/paysuper-billing-server/pkg/proto/billing"
import context "context"
import mock "github.com/stretchr/testify/mock"

// PriceTableServiceInterface is an autogenerated mock type for the PriceTableServiceInterface type
type PriceTableServiceInterface struct {
	mock.Mock
}

// GetByRegion provides a mock function with given fields: _a0, _a1
func (_m *PriceTableServiceInterface) GetByRegion(_a0 context.Context, _a1 string) (*billing.PriceTable, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *billing.PriceTable
	if rf, ok := ret.Get(0).(func(context.Context, string) *billing.PriceTable); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*billing.PriceTable)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Insert provides a mock function with given fields: _a0, _a1
func (_m *PriceTableServiceInterface) Insert(_a0 context.Context, _a1 *billing.PriceTable) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *billing.PriceTable) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
