// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import billing "github.com/paysuper/paysuper-billing-server/pkg/proto/billing"
import context "context"
import mock "github.com/stretchr/testify/mock"

// PaymentMethodInterface is an autogenerated mock type for the PaymentMethodInterface type
type PaymentMethodInterface struct {
	mock.Mock
}

// GetAll provides a mock function with given fields: ctx
func (_m *PaymentMethodInterface) GetAll(ctx context.Context) (map[string]*billing.PaymentMethod, error) {
	ret := _m.Called(ctx)

	var r0 map[string]*billing.PaymentMethod
	if rf, ok := ret.Get(0).(func(context.Context) map[string]*billing.PaymentMethod); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(map[string]*billing.PaymentMethod)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetByGroupAndCurrency provides a mock function with given fields: ctx, project, group, currency
func (_m *PaymentMethodInterface) GetByGroupAndCurrency(ctx context.Context, project *billing.Project, group string, currency string) (*billing.PaymentMethod, error) {
	ret := _m.Called(ctx, project, group, currency)

	var r0 *billing.PaymentMethod
	if rf, ok := ret.Get(0).(func(context.Context, *billing.Project, string, string) *billing.PaymentMethod); ok {
		r0 = rf(ctx, project, group, currency)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*billing.PaymentMethod)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, *billing.Project, string, string) error); ok {
		r1 = rf(ctx, project, group, currency)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetById provides a mock function with given fields: _a0, _a1
func (_m *PaymentMethodInterface) GetById(_a0 context.Context, _a1 string) (*billing.PaymentMethod, error) {
	ret := _m.Called(_a0, _a1)

	var r0 *billing.PaymentMethod
	if rf, ok := ret.Get(0).(func(context.Context, string) *billing.PaymentMethod); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*billing.PaymentMethod)
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

// GetPaymentSettings provides a mock function with given fields: paymentMethod, currency, mccCode, operatingCompanyId, paymentMethodBrand, project
func (_m *PaymentMethodInterface) GetPaymentSettings(paymentMethod *billing.PaymentMethod, currency string, mccCode string, operatingCompanyId string, paymentMethodBrand string, project *billing.Project) (*billing.PaymentMethodParams, error) {
	ret := _m.Called(paymentMethod, currency, mccCode, operatingCompanyId, paymentMethodBrand, project)

	var r0 *billing.PaymentMethodParams
	if rf, ok := ret.Get(0).(func(*billing.PaymentMethod, string, string, string, string, *billing.Project) *billing.PaymentMethodParams); ok {
		r0 = rf(paymentMethod, currency, mccCode, operatingCompanyId, paymentMethodBrand, project)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*billing.PaymentMethodParams)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(*billing.PaymentMethod, string, string, string, string, *billing.Project) error); ok {
		r1 = rf(paymentMethod, currency, mccCode, operatingCompanyId, paymentMethodBrand, project)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Insert provides a mock function with given fields: _a0, _a1
func (_m *PaymentMethodInterface) Insert(_a0 context.Context, _a1 *billing.PaymentMethod) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *billing.PaymentMethod) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// ListByParams provides a mock function with given fields: ctx, project, currency, mccCode, operatingCompanyId
func (_m *PaymentMethodInterface) ListByParams(ctx context.Context, project *billing.Project, currency string, mccCode string, operatingCompanyId string) ([]*billing.PaymentMethod, error) {
	ret := _m.Called(ctx, project, currency, mccCode, operatingCompanyId)

	var r0 []*billing.PaymentMethod
	if rf, ok := ret.Get(0).(func(context.Context, *billing.Project, string, string, string) []*billing.PaymentMethod); ok {
		r0 = rf(ctx, project, currency, mccCode, operatingCompanyId)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*billing.PaymentMethod)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, *billing.Project, string, string, string) error); ok {
		r1 = rf(ctx, project, currency, mccCode, operatingCompanyId)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MultipleInsert provides a mock function with given fields: _a0, _a1
func (_m *PaymentMethodInterface) MultipleInsert(_a0 context.Context, _a1 []*billing.PaymentMethod) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, []*billing.PaymentMethod) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Update provides a mock function with given fields: _a0, _a1
func (_m *PaymentMethodInterface) Update(_a0 context.Context, _a1 *billing.PaymentMethod) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *billing.PaymentMethod) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
