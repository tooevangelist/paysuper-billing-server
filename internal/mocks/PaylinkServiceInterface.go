// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import context "context"
import mock "github.com/stretchr/testify/mock"
import paylink "github.com/paysuper/paysuper-billing-server/pkg/proto/paylink"
import primitive "go.mongodb.org/mongo-driver/bson/primitive"

// PaylinkServiceInterface is an autogenerated mock type for the PaylinkServiceInterface type
type PaylinkServiceInterface struct {
	mock.Mock
}

// CountByQuery provides a mock function with given fields: ctx, query
func (_m *PaylinkServiceInterface) CountByQuery(ctx context.Context, query primitive.M) (int64, error) {
	ret := _m.Called(ctx, query)

	var r0 int64
	if rf, ok := ret.Get(0).(func(context.Context, primitive.M) int64); ok {
		r0 = rf(ctx, query)
	} else {
		r0 = ret.Get(0).(int64)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, primitive.M) error); ok {
		r1 = rf(ctx, query)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Delete provides a mock function with given fields: ctx, id, merchantId
func (_m *PaylinkServiceInterface) Delete(ctx context.Context, id string, merchantId string) error {
	ret := _m.Called(ctx, id, merchantId)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string) error); ok {
		r0 = rf(ctx, id, merchantId)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// GetById provides a mock function with given fields: ctx, id
func (_m *PaylinkServiceInterface) GetById(ctx context.Context, id string) (*paylink.Paylink, error) {
	ret := _m.Called(ctx, id)

	var r0 *paylink.Paylink
	if rf, ok := ret.Get(0).(func(context.Context, string) *paylink.Paylink); ok {
		r0 = rf(ctx, id)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*paylink.Paylink)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string) error); ok {
		r1 = rf(ctx, id)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetByIdAndMerchant provides a mock function with given fields: ctx, id, merchantId
func (_m *PaylinkServiceInterface) GetByIdAndMerchant(ctx context.Context, id string, merchantId string) (*paylink.Paylink, error) {
	ret := _m.Called(ctx, id, merchantId)

	var r0 *paylink.Paylink
	if rf, ok := ret.Get(0).(func(context.Context, string, string) *paylink.Paylink); ok {
		r0 = rf(ctx, id, merchantId)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*paylink.Paylink)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string, string) error); ok {
		r1 = rf(ctx, id, merchantId)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetListByQuery provides a mock function with given fields: ctx, query, limit, offset
func (_m *PaylinkServiceInterface) GetListByQuery(ctx context.Context, query primitive.M, limit int64, offset int64) ([]*paylink.Paylink, error) {
	ret := _m.Called(ctx, query, limit, offset)

	var r0 []*paylink.Paylink
	if rf, ok := ret.Get(0).(func(context.Context, primitive.M, int64, int64) []*paylink.Paylink); ok {
		r0 = rf(ctx, query, limit, offset)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*paylink.Paylink)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, primitive.M, int64, int64) error); ok {
		r1 = rf(ctx, query, limit, offset)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetPaylinkVisits provides a mock function with given fields: ctx, id, from, to
func (_m *PaylinkServiceInterface) GetPaylinkVisits(ctx context.Context, id string, from int64, to int64) (int64, error) {
	ret := _m.Called(ctx, id, from, to)

	var r0 int64
	if rf, ok := ret.Get(0).(func(context.Context, string, int64, int64) int64); ok {
		r0 = rf(ctx, id, from, to)
	} else {
		r0 = ret.Get(0).(int64)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string, int64, int64) error); ok {
		r1 = rf(ctx, id, from, to)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetUrl provides a mock function with given fields: ctx, id, merchantId, urlMask, utmSource, utmMedium, utmCampaign
func (_m *PaylinkServiceInterface) GetUrl(ctx context.Context, id string, merchantId string, urlMask string, utmSource string, utmMedium string, utmCampaign string) (string, error) {
	ret := _m.Called(ctx, id, merchantId, urlMask, utmSource, utmMedium, utmCampaign)

	var r0 string
	if rf, ok := ret.Get(0).(func(context.Context, string, string, string, string, string, string) string); ok {
		r0 = rf(ctx, id, merchantId, urlMask, utmSource, utmMedium, utmCampaign)
	} else {
		r0 = ret.Get(0).(string)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string, string, string, string, string, string) error); ok {
		r1 = rf(ctx, id, merchantId, urlMask, utmSource, utmMedium, utmCampaign)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IncrVisits provides a mock function with given fields: ctx, id
func (_m *PaylinkServiceInterface) IncrVisits(ctx context.Context, id string) error {
	ret := _m.Called(ctx, id)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string) error); ok {
		r0 = rf(ctx, id)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Insert provides a mock function with given fields: ctx, pl
func (_m *PaylinkServiceInterface) Insert(ctx context.Context, pl *paylink.Paylink) error {
	ret := _m.Called(ctx, pl)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *paylink.Paylink) error); ok {
		r0 = rf(ctx, pl)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Update provides a mock function with given fields: ctx, pl
func (_m *PaylinkServiceInterface) Update(ctx context.Context, pl *paylink.Paylink) error {
	ret := _m.Called(ctx, pl)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *paylink.Paylink) error); ok {
		r0 = rf(ctx, pl)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// UpdatePaylinkTotalStat provides a mock function with given fields: ctx, id, merchantId
func (_m *PaylinkServiceInterface) UpdatePaylinkTotalStat(ctx context.Context, id string, merchantId string) error {
	ret := _m.Called(ctx, id, merchantId)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string) error); ok {
		r0 = rf(ctx, id, merchantId)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
