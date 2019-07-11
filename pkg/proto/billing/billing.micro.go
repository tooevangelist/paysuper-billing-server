// Code generated by protoc-gen-micro. DO NOT EDIT.
// source: billing/billing.proto

/*
Package billing is a generated protocol buffer package.

It is generated from these files:
	billing/billing.proto

It has these top-level messages:
	Name
	OrderCreateRequest
	Project
	ProjectOrder
	MerchantContact
	MerchantContactTechnical
	MerchantContactAuthorized
	MerchantBanking
	MerchantLastPayout
	MerchantUser
	Merchant
	SystemNotificationStatuses
	Notification
	OrderPlatformFee
	OrderTax
	OrderBillingAddress
	OrderUser
	Order
	CountryRestriction
	OrderItem
	OrderPaginate
	PaymentMethodOrder
	PaymentMethodParams
	PaymentSystem
	PaymentMethodCard
	PaymentMethodWallet
	PaymentMethodCrypto
	ProjectPaymentMethod
	PaymentMethod
	Vat
	Commission
	CardExpire
	SavedCard
	PaymentFormPaymentMethod
	MerchantPaymentMethodPerTransactionCommission
	MerchantPaymentMethodCommissions
	MerchantPaymentMethodIntegration
	MerchantPaymentMethodIdentification
	MerchantPaymentMethod
	RefundPayerData
	RefundOrder
	Refund
	MerchantPaymentMethodHistory
	CustomerIdentity
	CustomerIpHistory
	CustomerAddressHistory
	CustomerStringValueHistory
	Customer
	TokenUserEmailValue
	TokenUserPhoneValue
	TokenUserIpValue
	TokenUserLocaleValue
	TokenUserValue
	TokenUser
	TokenSettingsReturnUrl
	TokenSettingsItem
	TokenSettings
	OrderIssuer
	OrderNotificationRefund
	GetCountryRequest
	CountryVatThreshold
	Country
	CountriesList
	GetPriceGroupRequest
	PriceGroup
	ZipCodeState
	ZipCode
	PaymentChannelCostSystem
	PaymentChannelCostSystemRequest
	PaymentChannelCostSystemList
	PaymentChannelCostMerchant
	PaymentChannelCostMerchantRequest
	PaymentChannelCostMerchantList
	PaymentChannelCostMerchantListRequest
	MoneyBackCostSystem
	MoneyBackCostSystemRequest
	MoneyBackCostSystemList
	MoneyBackCostMerchant
	MoneyBackCostMerchantRequest
	PaymentCostDeleteRequest
	MoneyBackCostMerchantList
	MoneyBackCostMerchantListRequest
	PayoutCostSystem
	AccountingEntrySource
	AccountingEntry
	RoyaltyReportDetails
	RoyaltyReportCorrection
	RoyaltyReport
	RoyaltyReportChanges
	RoyaltyReportOrder
	VatTransaction
	VatReport
	AnnualTurnover
	OrderViewMoney
	OrderViewPublic
	OrderViewPrivate
*/
package billing

import proto "github.com/golang/protobuf/proto"
import fmt "fmt"
import math "math"
import _ "github.com/golang/protobuf/ptypes/timestamp"

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion2 // please upgrade the proto package
