// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import billing "github.com/paysuper/paysuper-billing-server/pkg/proto/billing"
import context "context"
import grpc "github.com/paysuper/paysuper-billing-server/pkg/proto/grpc"
import mock "github.com/stretchr/testify/mock"

// PriceGroupServiceInterface is an autogenerated mock type for the PriceGroupServiceInterface type
type PriceGroupServiceInterface struct {
	mock.Mock
}

// CalculatePriceWithFraction provides a mock function with given fields: _a0, _a1
func (_m *PriceGroupServiceInterface) CalculatePriceWithFraction(_a0 float64, _a1 float64) float64 {
	ret := _m.Called(_a0, _a1)

	var r0 float64
	if rf, ok := ret.Get(0).(func(float64, float64) float64); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Get(0).(float64)
	}

	return r0
}

// GetAll provides a mock function with given fields: _a0
func (_m *PriceGroupServiceInterface) GetAll(_a0 context.Context) ([]*billing.PriceGroup, error) {
	ret := _m.Called(_a0)

	var r0 []*billing.PriceGroup
	if rf, ok := ret.Get(0).(func(context.Context) []*billing.PriceGroup); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*billing.PriceGroup)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetById provides a mock function with given fields: _a0, _a1
func (_m *PriceGroupServiceInterface) GetById(_a0 context.Context, _a1 string) (*billing.PriceGroup, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *billing.PriceGroup
	if rf, ok := ret.Get(0).(func(context.Context, string) *billing.PriceGroup); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*billing.PriceGroup)
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

// GetByRegion provides a mock function with given fields: _a0, _a1
func (_m *PriceGroupServiceInterface) GetByRegion(_a0 context.Context, _a1 string) (*billing.PriceGroup, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *billing.PriceGroup
	if rf, ok := ret.Get(0).(func(context.Context, string) *billing.PriceGroup); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*billing.PriceGroup)
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
func (_m *PriceGroupServiceInterface) Insert(_a0 context.Context, _a1 *billing.PriceGroup) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *billing.PriceGroup) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// MakeCurrencyList provides a mock function with given fields: _a0, _a1
func (_m *PriceGroupServiceInterface) MakeCurrencyList(_a0 []*billing.PriceGroup, _a1 *billing.CountriesList) []*grpc.PriceGroupRegions {
	ret := _m.Called(_a0, _a1)

	var r0 []*grpc.PriceGroupRegions
	if rf, ok := ret.Get(0).(func([]*billing.PriceGroup, *billing.CountriesList) []*grpc.PriceGroupRegions); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*grpc.PriceGroupRegions)
		}
	}

	return r0
}

// MultipleInsert provides a mock function with given fields: _a0, _a1
func (_m *PriceGroupServiceInterface) MultipleInsert(_a0 context.Context, _a1 []*billing.PriceGroup) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, []*billing.PriceGroup) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Update provides a mock function with given fields: _a0, _a1
func (_m *PriceGroupServiceInterface) Update(_a0 context.Context, _a1 *billing.PriceGroup) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *billing.PriceGroup) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
