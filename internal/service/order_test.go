package service

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/go-redis/redis"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	"github.com/google/uuid"
	"github.com/jinzhu/now"
	casbinMocks "github.com/paysuper/casbin-server/pkg/mocks"
	"github.com/paysuper/paysuper-billing-server/internal/config"
	"github.com/paysuper/paysuper-billing-server/internal/database"
	"github.com/paysuper/paysuper-billing-server/internal/mocks"
	"github.com/paysuper/paysuper-billing-server/pkg"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/billing"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/grpc"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/paylink"
	"github.com/paysuper/paysuper-recurring-repository/pkg/constant"
	postmarkSdrPkg "github.com/paysuper/postmark-sender/pkg"
	"github.com/stoewer/go-strcase"
	"github.com/streadway/amqp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gopkg.in/ProtocolONE/rabbitmq.v1/pkg"
	mongodb "gopkg.in/paysuper/paysuper-database-mongo.v1"
	"net"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type OrderTestSuite struct {
	suite.Suite
	service *Service
	cache   CacheInterface
	log     *zap.Logger

	project                                *billing.Project
	projectFixedAmount                     *billing.Project
	projectWithProducts                    *billing.Project
	projectWithProductsInVirtualCurrency   *billing.Project
	projectWithKeyProducts                 *billing.Project
	inactiveProject                        *billing.Project
	projectWithoutPaymentMethods           *billing.Project
	projectIncorrectPaymentMethodId        *billing.Project
	projectEmptyPaymentMethodTerminal      *billing.Project
	projectUahLimitCurrency                *billing.Project
	paymentMethod                          *billing.PaymentMethod
	inactivePaymentMethod                  *billing.PaymentMethod
	paymentMethodWithInactivePaymentSystem *billing.PaymentMethod
	paymentMethodQiwi                      *billing.PaymentMethod
	pmWebMoney                             *billing.PaymentMethod
	pmBitcoin1                             *billing.PaymentMethod
	pmBitcoin2                             *billing.PaymentMethod
	productIds                             []string
	productIdsWithVirtualCurrency          []string
	keyProductIds                          []string
	merchantDefaultCurrency                string
	paymentMethodWithoutCommission         *billing.PaymentMethod
	paylink1                               *paylink.Paylink
	paylink2                               *paylink.Paylink // deleted paylink
	paylink3                               *paylink.Paylink // expired paylink
	operatingCompany                       *billing.OperatingCompany

	logObserver *zap.Logger
	zapRecorder *observer.ObservedLogs
}

func Test_Order(t *testing.T) {
	suite.Run(t, new(OrderTestSuite))
}

func (suite *OrderTestSuite) SetupTest() {
	cfg, err := config.NewConfig()
	if err != nil {
		suite.FailNow("Config load failed", "%v", err)
	}
	cfg.CardPayApiUrl = "https://sandbox.cardpay.com"

	db, err := mongodb.NewDatabase()
	if err != nil {
		suite.FailNow("Database connection failed", "%v", err)
	}

	paymentMinLimitSystem1 := &billing.PaymentMinLimitSystem{
		Id:        primitive.NewObjectID().Hex(),
		Currency:  "RUB",
		Amount:    0.01,
		CreatedAt: ptypes.TimestampNow(),
		UpdatedAt: ptypes.TimestampNow(),
	}

	paymentMinLimitSystem2 := &billing.PaymentMinLimitSystem{
		Id:        primitive.NewObjectID().Hex(),
		Currency:  "USD",
		Amount:    0.01,
		CreatedAt: ptypes.TimestampNow(),
		UpdatedAt: ptypes.TimestampNow(),
	}

	paymentMinLimitSystem3 := &billing.PaymentMinLimitSystem{
		Id:        primitive.NewObjectID().Hex(),
		Currency:  "UAH",
		Amount:    0.01,
		CreatedAt: ptypes.TimestampNow(),
		UpdatedAt: ptypes.TimestampNow(),
	}

	paymentMinLimitSystem4 := &billing.PaymentMinLimitSystem{
		Id:        primitive.NewObjectID().Hex(),
		Currency:  "KZT",
		Amount:    0.01,
		CreatedAt: ptypes.TimestampNow(),
		UpdatedAt: ptypes.TimestampNow(),
	}

	paymentMinLimitSystem5 := &billing.PaymentMinLimitSystem{
		Id:        primitive.NewObjectID().Hex(),
		Currency:  "EUR",
		Amount:    90,
		CreatedAt: ptypes.TimestampNow(),
		UpdatedAt: ptypes.TimestampNow(),
	}

	suite.operatingCompany = &billing.OperatingCompany{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "Legal name",
		Country:            "RU",
		RegistrationNumber: "some number",
		VatNumber:          "some vat number",
		Address:            "Home, home 0",
		VatAddress:         "Address for VAT purposes",
		SignatoryName:      "Vassiliy Poupkine",
		SignatoryPosition:  "CEO",
		BankingDetails:     "bank details including bank, bank address, account number, swift/ bic, intermediary bank",
		PaymentCountries:   []string{},
	}

	_, err = db.Collection(collectionOperatingCompanies).InsertOne(context.TODO(), suite.operatingCompany)
	if err != nil {
		suite.FailNow("Insert operatingCompany test data failed", "%v", err)
	}

	keyRubVisa := pkg.GetPaymentMethodKey("RUB", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Visa")
	keyUsdVisa := pkg.GetPaymentMethodKey("USD", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Visa")
	keyUahVisa := pkg.GetPaymentMethodKey("UAH", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Visa")

	keyRubBitcoin := pkg.GetPaymentMethodKey("RUB", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Bitcoin")
	keyUsdBitcoin := pkg.GetPaymentMethodKey("USD", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Bitcoin")
	keyUahBitcoin := pkg.GetPaymentMethodKey("UAH", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Bitcoin")

	keyRubQiwi := pkg.GetPaymentMethodKey("RUB", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Qiwi")
	keyUsdQiwi := pkg.GetPaymentMethodKey("USD", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Qiwi")
	keyUahQiwi := pkg.GetPaymentMethodKey("UAH", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Qiwi")

	keyRubWebmoney := pkg.GetPaymentMethodKey("RUB", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Webmoney")
	keyUsdWebmoney := pkg.GetPaymentMethodKey("USD", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Webmoney")
	keyUahWebmoney := pkg.GetPaymentMethodKey("UAH", pkg.MccCodeLowRisk, suite.operatingCompany.Id, "Webmoney")

	pgRub := &billing.PriceGroup{
		Id:       primitive.NewObjectID().Hex(),
		Region:   "RUB",
		Currency: "RUB",
		IsActive: true,
	}
	pgUsd := &billing.PriceGroup{
		Id:       primitive.NewObjectID().Hex(),
		Region:   "USD",
		Currency: "USD",
		IsActive: true,
	}
	pgCis := &billing.PriceGroup{
		Id:       primitive.NewObjectID().Hex(),
		Region:   "CIS",
		Currency: "USD",
		IsActive: true,
	}
	pgUah := &billing.PriceGroup{
		Id:       primitive.NewObjectID().Hex(),
		Region:   "UAH",
		Currency: "UAH",
		IsActive: true,
	}

	ru := &billing.Country{
		IsoCodeA2:       "RU",
		Region:          "Russia",
		Currency:        "RUB",
		PaymentsAllowed: true,
		ChangeAllowed:   true,
		VatEnabled:      true,
		PriceGroupId:    pgRub.Id,
		VatCurrency:     "RUB",
		VatThreshold: &billing.CountryVatThreshold{
			Year:  0,
			World: 0,
		},
		VatPeriodMonth:         3,
		VatDeadlineDays:        25,
		VatStoreYears:          5,
		VatCurrencyRatesPolicy: "last-day",
		VatCurrencyRatesSource: "cbrf",
		PayerTariffRegion:      pkg.TariffRegionRussiaAndCis,
	}
	us := &billing.Country{
		IsoCodeA2:       "US",
		Region:          "North America",
		Currency:        "USD",
		PaymentsAllowed: true,
		ChangeAllowed:   true,
		VatEnabled:      true,
		PriceGroupId:    pgUsd.Id,
		VatCurrency:     "USD",
		VatThreshold: &billing.CountryVatThreshold{
			Year:  0,
			World: 0,
		},
		VatPeriodMonth:         0,
		VatDeadlineDays:        0,
		VatStoreYears:          0,
		VatCurrencyRatesPolicy: "",
		VatCurrencyRatesSource: "",
		PayerTariffRegion:      pkg.TariffRegionWorldwide,
	}
	by := &billing.Country{
		IsoCodeA2:       "BY",
		Region:          "CIS",
		Currency:        "USD",
		PaymentsAllowed: false,
		ChangeAllowed:   false,
		VatEnabled:      true,
		PriceGroupId:    pgCis.Id,
		VatCurrency:     "BYN",
		VatThreshold: &billing.CountryVatThreshold{
			Year:  0,
			World: 0,
		},
		VatPeriodMonth:         3,
		VatDeadlineDays:        25,
		VatStoreYears:          5,
		VatCurrencyRatesPolicy: "last-day",
		VatCurrencyRatesSource: "cbrf",
		PayerTariffRegion:      pkg.TariffRegionRussiaAndCis,
	}
	ua := &billing.Country{
		IsoCodeA2:       "UA",
		Region:          "CIS",
		Currency:        "UAH",
		PaymentsAllowed: false,
		ChangeAllowed:   true,
		VatEnabled:      false,
		PriceGroupId:    pgCis.Id,
		VatCurrency:     "",
		VatThreshold: &billing.CountryVatThreshold{
			Year:  0,
			World: 0,
		},
		VatPeriodMonth:         3,
		VatDeadlineDays:        25,
		VatStoreYears:          5,
		VatCurrencyRatesPolicy: "last-day",
		VatCurrencyRatesSource: "cbrf",
		PayerTariffRegion:      pkg.TariffRegionRussiaAndCis,
	}
	it := &billing.Country{
		IsoCodeA2:       "IT",
		Region:          "UAH",
		Currency:        "UAH",
		PaymentsAllowed: true,
		ChangeAllowed:   true,
		VatEnabled:      true,
		PriceGroupId:    pgUah.Id,
		VatCurrency:     "",
		VatThreshold: &billing.CountryVatThreshold{
			Year:  0,
			World: 0,
		},
		VatPeriodMonth:         3,
		VatDeadlineDays:        25,
		VatStoreYears:          5,
		VatCurrencyRatesPolicy: "last-day",
		VatCurrencyRatesSource: "cbrf",
		PayerTariffRegion:      pkg.TariffRegionRussiaAndCis,
	}

	ps0 := &billing.PaymentSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "CardPay",
		AccountingCurrency: "RUB",
		AccountingPeriod:   "every-day",
		Country:            "",
		IsActive:           true,
		Handler:            "cardpay_mock",
	}

	pmBankCardNotUsed := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Bank card NEVER USING",
		Group:            "BANKCARD",
		MinPaymentAmount: 90,
		MaxPaymentAmount: 15000,
		ExternalId:       "BANKCARD",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
			keyUsdVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyUsdVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
		},
		Type:            "bank_card",
		IsActive:        true,
		AccountRegexp:   "^(?:4[0-9]{12}(?:[0-9]{3})?|[25][1-7][0-9]{14}|6(?:011|5[0-9][0-9])[0-9]{12}|3[47][0-9]{13}|3(?:0[0-5]|[68][0-9])[0-9]{11}|(?:2131|1800|35\\d{3})\\d{11})$",
		PaymentSystemId: ps0.Id,
	}

	ps1 := &billing.PaymentSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "CardPay",
		AccountingCurrency: "RUB",
		AccountingPeriod:   "every-day",
		Country:            "",
		IsActive:           true,
		Handler:            "cardpay_mock",
	}

	pmBankCard := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Bank card",
		Group:            "BANKCARD",
		MinPaymentAmount: 100,
		MaxPaymentAmount: 15000,
		ExternalId:       "BANKCARD",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
			keyUsdVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
			keyUahVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "UAH",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyUsdVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
			keyRubVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
			keyUahVisa: {
				TerminalId:         "15985",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				Currency:           "UAH",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"VISA", "MASTERCARD"},
			},
		},
		Type:            "bank_card",
		IsActive:        true,
		AccountRegexp:   "^(?:4[0-9]{12}(?:[0-9]{3})?|[25][1-7][0-9]{14}|6(?:011|5[0-9][0-9])[0-9]{12}|3[47][0-9]{13}|3(?:0[0-5]|[68][0-9])[0-9]{11}|(?:2131|1800|35\\d{3})\\d{11})$",
		PaymentSystemId: ps1.Id,
	}

	ps2 := &billing.PaymentSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "CardPay",
		AccountingCurrency: "RUB",
		AccountingPeriod:   "every-day",
		Country:            "",
		IsActive:           true,
		Handler:            "cardpay2",
	}

	pmBitcoin1 := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Bitcoin",
		Group:            "BITCOIN_1",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "BITCOIN",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubBitcoin: {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN"},
			},
			keyUsdBitcoin: {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyRubBitcoin: {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN"},
			},
		},
		Type:            "crypto",
		IsActive:        true,
		PaymentSystemId: ps2.Id,
	}
	pmBitcoin2 := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Bitcoin 2",
		Group:            "BITCOIN_2",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "BITCOIN",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubBitcoin + " 2": {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN 2"},
			},
			keyUsdBitcoin + " 2": {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN 2"},
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyRubBitcoin + " 2": {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN 2"},
			},
		},
		Type:            "crypto",
		IsActive:        true,
		PaymentSystemId: ps2.Id,
	}

	ps3 := &billing.PaymentSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "CardPay 2",
		AccountingCurrency: "UAH",
		AccountingPeriod:   "every-day",
		Country:            "",
		IsActive:           false,
		Handler:            "cardpay_mock",
	}

	pmQiwi := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Qiwi",
		Group:            "QIWI",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "QIWI",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubQiwi: {
				TerminalId:         "15993",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"QIWI"},
			},
			keyUsdQiwi: {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"QIWI"},
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyRubQiwi: {
				TerminalId:         "15993",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"QIWI"},
			},
			keyUahQiwi: {
				TerminalId:         "15993",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "UAH",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"QIWI"},
			},
		},
		Type:            "ewallet",
		IsActive:        true,
		AccountRegexp:   "^\\d{1,15}",
		PaymentSystemId: ps3.Id,
	}

	pmQiwiActive := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Qiwi",
		Group:            "QIWI",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "QIWI",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubQiwi: {
				TerminalId:         "15993",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"QIWI"},
			},
			keyUsdQiwi: {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"QIWI"},
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyRubQiwi: {
				TerminalId:         "15993",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"QIWI"},
			},
			keyUahQiwi: {
				TerminalId:         "15993",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "UAH",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"QIWI"},
			},
		},
		Type:            "ewallet",
		IsActive:        true,
		AccountRegexp:   "^\\d{1,15}",
		PaymentSystemId: ps1.Id,
	}

	date, err := ptypes.TimestampProto(time.Now().Add(time.Hour * -360))

	if err != nil {
		suite.FailNow("Generate merchant date failed", "%v", err)
	}

	merchant := &billing.Merchant{
		Id: primitive.NewObjectID().Hex(),
		Company: &billing.MerchantCompanyInfo{
			Name:               "merchant1",
			AlternativeName:    "merchant1",
			Website:            "http://localhost",
			Country:            "RU",
			Zip:                "190000",
			City:               "St.Petersburg",
			Address:            "address",
			AddressAdditional:  "address_additional",
			RegistrationNumber: "registration_number",
		},
		Contacts: &billing.MerchantContact{
			Authorized: &billing.MerchantContactAuthorized{
				Name:     "Unit Test",
				Email:    "test@unit.test",
				Phone:    "123456789",
				Position: "Unit Test",
			},
			Technical: &billing.MerchantContactTechnical{
				Name:  "Unit Test",
				Email: "test@unit.test",
				Phone: "123456789",
			},
		},
		Banking: &billing.MerchantBanking{
			Currency:             "USD",
			Name:                 "Bank name",
			Address:              "address",
			AccountNumber:        "0000001",
			Swift:                "swift",
			CorrespondentAccount: "correspondent_account",
			Details:              "details",
		},
		IsVatEnabled:              true,
		IsCommissionToUserEnabled: true,
		Status:                    pkg.MerchantStatusDraft,
		LastPayout: &billing.MerchantLastPayout{
			Date:   date,
			Amount: 999999,
		},
		IsSigned: true,
		PaymentMethods: map[string]*billing.MerchantPaymentMethod{
			pmBankCard.Id: {
				PaymentMethod: &billing.MerchantPaymentMethodIdentification{
					Id:   pmBankCard.Id,
					Name: pmBankCard.Name,
				},
				Commission: &billing.MerchantPaymentMethodCommissions{
					Fee: 2.5,
					PerTransaction: &billing.MerchantPaymentMethodPerTransactionCommission{
						Fee:      30,
						Currency: "RUB",
					},
				},
				Integration: &billing.MerchantPaymentMethodIntegration{
					TerminalId:               "15985",
					TerminalPassword:         "A1tph4I6BD0f",
					TerminalCallbackPassword: "0V1rJ7t4jCRv",
					Integrated:               true,
				},
				IsActive: true,
			},
			pmBitcoin1.Id: {
				PaymentMethod: &billing.MerchantPaymentMethodIdentification{
					Id:   pmBitcoin1.Id,
					Name: pmBitcoin1.Name,
				},
				Commission: &billing.MerchantPaymentMethodCommissions{
					Fee: 3.5,
					PerTransaction: &billing.MerchantPaymentMethodPerTransactionCommission{
						Fee:      300,
						Currency: "RUB",
					},
				},
				Integration: &billing.MerchantPaymentMethodIntegration{
					TerminalId:       "1234567890",
					TerminalPassword: "0987654321",
					Integrated:       true,
				},
				IsActive: true,
			},
			pmQiwiActive.Id: {
				PaymentMethod: &billing.MerchantPaymentMethodIdentification{
					Id:   pmQiwiActive.Id,
					Name: pmQiwiActive.Name,
				},
				Commission: &billing.MerchantPaymentMethodCommissions{
					Fee: 3.5,
					PerTransaction: &billing.MerchantPaymentMethodPerTransactionCommission{
						Fee:      300,
						Currency: "RUB",
					},
				},
				Integration: &billing.MerchantPaymentMethodIntegration{
					TerminalId:       "1234567890",
					TerminalPassword: "0987654321",
					Integrated:       true,
				},
				IsActive: true,
			},
		},
		Tariff: &billing.MerchantTariff{
			Payment: []*billing.MerchantTariffRatesPayment{
				{
					MinAmount:              0,
					MaxAmount:              4.99,
					MethodName:             "VISA",
					MethodPercentFee:       1.8,
					MethodFixedFee:         0.2,
					MethodFixedFeeCurrency: "USD",
					PsPercentFee:           3.0,
					PsFixedFee:             0.3,
					PsFixedFeeCurrency:     "USD",
					MerchantHomeRegion:     "russia_and_cis",
					PayerRegion:            "europe",
				},
				{
					MinAmount:              5,
					MaxAmount:              999999999.99,
					MethodName:             "MasterCard",
					MethodPercentFee:       1.8,
					MethodFixedFee:         0.2,
					MethodFixedFeeCurrency: "USD",
					PsPercentFee:           3.0,
					PsFixedFee:             0.3,
					PsFixedFeeCurrency:     "USD",
					MerchantHomeRegion:     "russia_and_cis",
					PayerRegion:            "europe",
				},
			},
			Payout: &billing.MerchantTariffRatesSettingsItem{
				MethodPercentFee:       0,
				MethodFixedFee:         25.0,
				MethodFixedFeeCurrency: "EUR",
				IsPaidByMerchant:       true,
			},
			HomeRegion: "russia_and_cis",
		},
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}

	merchantAgreement := &billing.Merchant{
		Id: primitive.NewObjectID().Hex(),
		Company: &billing.MerchantCompanyInfo{
			Name:               "merchant1",
			AlternativeName:    "merchant1",
			Website:            "http://localhost",
			Country:            "RU",
			Zip:                "190000",
			City:               "St.Petersburg",
			Address:            "address",
			AddressAdditional:  "address_additional",
			RegistrationNumber: "registration_number",
		},
		Contacts: &billing.MerchantContact{
			Authorized: &billing.MerchantContactAuthorized{
				Name:     "Unit Test",
				Email:    "test@unit.test",
				Phone:    "123456789",
				Position: "Unit Test",
			},
			Technical: &billing.MerchantContactTechnical{
				Name:  "Unit Test",
				Email: "test@unit.test",
				Phone: "123456789",
			},
		},
		Banking: &billing.MerchantBanking{
			Currency:             "RUB",
			Name:                 "Bank name",
			Address:              "address",
			AccountNumber:        "0000001",
			Swift:                "swift",
			CorrespondentAccount: "correspondent_account",
			Details:              "details",
		},
		IsVatEnabled:              true,
		IsCommissionToUserEnabled: true,
		Status:                    pkg.MerchantStatusAgreementSigning,
		LastPayout: &billing.MerchantLastPayout{
			Date:   date,
			Amount: 10000,
		},
		IsSigned: true,
		Tariff: &billing.MerchantTariff{
			Payment: []*billing.MerchantTariffRatesPayment{
				{
					MinAmount:              0,
					MaxAmount:              4.99,
					MethodName:             "VISA",
					MethodPercentFee:       1.8,
					MethodFixedFee:         0.2,
					MethodFixedFeeCurrency: "USD",
					PsPercentFee:           3.0,
					PsFixedFee:             0.3,
					PsFixedFeeCurrency:     "USD",
					MerchantHomeRegion:     "russia_and_cis",
					PayerRegion:            "europe",
				},
				{
					MinAmount:              5,
					MaxAmount:              999999999.99,
					MethodName:             "MasterCard",
					MethodPercentFee:       1.8,
					MethodFixedFee:         0.2,
					MethodFixedFeeCurrency: "USD",
					PsPercentFee:           3.0,
					PsFixedFee:             0.3,
					PsFixedFeeCurrency:     "USD",
					MerchantHomeRegion:     "russia_and_cis",
					PayerRegion:            "europe",
				},
			},
			Payout: &billing.MerchantTariffRatesSettingsItem{
				MethodPercentFee:       0,
				MethodFixedFee:         25.0,
				MethodFixedFeeCurrency: "EUR",
				IsPaidByMerchant:       true,
			},
			HomeRegion: "russia_and_cis",
		},
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}
	merchant1 := &billing.Merchant{
		Id: primitive.NewObjectID().Hex(),
		Company: &billing.MerchantCompanyInfo{
			Name:               "merchant1",
			AlternativeName:    "merchant1",
			Website:            "http://localhost",
			Country:            "RU",
			Zip:                "190000",
			City:               "St.Petersburg",
			Address:            "address",
			AddressAdditional:  "address_additional",
			RegistrationNumber: "registration_number",
		},
		Contacts: &billing.MerchantContact{
			Authorized: &billing.MerchantContactAuthorized{
				Name:     "Unit Test",
				Email:    "test@unit.test",
				Phone:    "123456789",
				Position: "Unit Test",
			},
			Technical: &billing.MerchantContactTechnical{
				Name:  "Unit Test",
				Email: "test@unit.test",
				Phone: "123456789",
			},
		},
		Banking: &billing.MerchantBanking{
			Currency:             "RUB",
			Name:                 "Bank name",
			Address:              "address",
			AccountNumber:        "0000001",
			Swift:                "swift",
			CorrespondentAccount: "correspondent_account",
			Details:              "details",
		},
		IsVatEnabled:              true,
		IsCommissionToUserEnabled: true,
		Status:                    pkg.MerchantStatusDraft,
		LastPayout: &billing.MerchantLastPayout{
			Date:   date,
			Amount: 100000,
		},
		IsSigned: false,
		Tariff: &billing.MerchantTariff{
			Payment: []*billing.MerchantTariffRatesPayment{
				{
					MinAmount:              0,
					MaxAmount:              4.99,
					MethodName:             "VISA",
					MethodPercentFee:       1.8,
					MethodFixedFee:         0.2,
					MethodFixedFeeCurrency: "USD",
					PsPercentFee:           3.0,
					PsFixedFee:             0.3,
					PsFixedFeeCurrency:     "USD",
					MerchantHomeRegion:     "russia_and_cis",
					PayerRegion:            "europe",
				},
				{
					MinAmount:              5,
					MaxAmount:              999999999.99,
					MethodName:             "MasterCard",
					MethodPercentFee:       1.8,
					MethodFixedFee:         0.2,
					MethodFixedFeeCurrency: "USD",
					PsPercentFee:           3.0,
					PsFixedFee:             0.3,
					PsFixedFeeCurrency:     "USD",
					MerchantHomeRegion:     "russia_and_cis",
					PayerRegion:            "europe",
				},
			},
			Payout: &billing.MerchantTariffRatesSettingsItem{
				MethodPercentFee:       0,
				MethodFixedFee:         25.0,
				MethodFixedFeeCurrency: "EUR",
				IsPaidByMerchant:       true,
			},
			HomeRegion: "russia_and_cis",
		},
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}

	project := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         "default",
		LimitsCurrency:           "USD",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       false,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project 1 secret key",
		Status:                   pkg.ProjectStatusInProduction,
		MerchantId:               merchant.Id,
	}
	projectFixedAmount := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         "default",
		LimitsCurrency:           "USD",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       false,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project 1 secret key",
		Status:                   pkg.ProjectStatusDraft,
		MerchantId:               merchant.Id,
	}

	projectWithProductsInVirtualCurrency := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         "default",
		LimitsCurrency:           "USD",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       true,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project X secret key",
		Status:                   pkg.ProjectStatusInProduction,
		MerchantId:               merchant.Id,
		VirtualCurrency: &billing.ProjectVirtualCurrency{
			Name: map[string]string{"en": "test project 1"},
			Prices: []*billing.ProductPrice{
				{
					Currency: "USD",
					Region:   "USD",
					Amount:   10,
				},
				{
					Currency: "RUB",
					Region:   "RUB",
					Amount:   650,
				},
			},
		},
	}

	projectWithProducts := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         "default",
		LimitsCurrency:           "USD",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       true,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project 1 secret key",
		Status:                   pkg.ProjectStatusDraft,
		MerchantId:               merchant.Id,
	}
	projectWithKeyProducts := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         "default",
		LimitsCurrency:           "USD",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test key project"},
		IsProductsCheckout:       false,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test key project secret key",
		Status:                   pkg.ProjectStatusDraft,
		MerchantId:               merchant.Id,
	}
	projectUahLimitCurrency := &billing.Project{
		Id:                 primitive.NewObjectID().Hex(),
		CallbackCurrency:   "RUB",
		CallbackProtocol:   "default",
		LimitsCurrency:     "UAH",
		MaxPaymentAmount:   15000,
		MinPaymentAmount:   0,
		Name:               map[string]string{"en": "project uah limit currency"},
		IsProductsCheckout: true,
		SecretKey:          "project uah limit currency secret key",
		Status:             pkg.ProjectStatusInProduction,
		MerchantId:         merchant1.Id,
	}
	projectIncorrectPaymentMethodId := &billing.Project{
		Id:                 primitive.NewObjectID().Hex(),
		CallbackCurrency:   "RUB",
		CallbackProtocol:   "default",
		LimitsCurrency:     "RUB",
		MaxPaymentAmount:   15000,
		MinPaymentAmount:   0,
		Name:               map[string]string{"en": "project incorrect payment Method id"},
		IsProductsCheckout: true,
		SecretKey:          "project incorrect payment Method id secret key",
		Status:             pkg.ProjectStatusInProduction,
		MerchantId:         merchant1.Id,
	}
	projectEmptyPaymentMethodTerminal := &billing.Project{
		Id:                 primitive.NewObjectID().Hex(),
		MerchantId:         merchant1.Id,
		CallbackCurrency:   "RUB",
		CallbackProtocol:   "default",
		LimitsCurrency:     "RUB",
		MaxPaymentAmount:   15000,
		MinPaymentAmount:   0,
		Name:               map[string]string{"en": "project incorrect payment Method id"},
		IsProductsCheckout: false,
		SecretKey:          "project incorrect payment Method id secret key",
		Status:             pkg.ProjectStatusInProduction,
	}
	projectWithoutPaymentMethods := &billing.Project{
		Id:                 primitive.NewObjectID().Hex(),
		MerchantId:         merchant1.Id,
		CallbackCurrency:   "RUB",
		CallbackProtocol:   "default",
		LimitsCurrency:     "RUB",
		MaxPaymentAmount:   15000,
		MinPaymentAmount:   0,
		Name:               map[string]string{"en": "test project 1"},
		IsProductsCheckout: true,
		SecretKey:          "test project 1 secret key",
		Status:             pkg.ProjectStatusInProduction,
	}
	inactiveProject := &billing.Project{
		Id:                 primitive.NewObjectID().Hex(),
		MerchantId:         merchant1.Id,
		CallbackCurrency:   "RUB",
		CallbackProtocol:   "xsolla",
		LimitsCurrency:     "RUB",
		MaxPaymentAmount:   15000,
		MinPaymentAmount:   0,
		Name:               map[string]string{"en": "test project 2"},
		IsProductsCheckout: true,
		SecretKey:          "test project 2 secret key",
		Status:             pkg.ProjectStatusDeleted,
	}
	projects := []*billing.Project{
		project,
		projectFixedAmount,
		projectWithProducts,
		inactiveProject,
		projectWithoutPaymentMethods,
		projectIncorrectPaymentMethodId,
		projectEmptyPaymentMethodTerminal,
		projectUahLimitCurrency,
		projectWithKeyProducts,
		projectWithProductsInVirtualCurrency,
	}

	ps4 := &billing.PaymentSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "CardPay",
		AccountingCurrency: "RUB",
		AccountingPeriod:   "every-day",
		Country:            "",
		IsActive:           true,
		Handler:            "cardpay",
	}

	pmWebMoney := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "WebMoney",
		Group:            "WEBMONEY",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "WEBMONEY",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubWebmoney: {
				TerminalId:         "15985",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"WEBMONEY"},
			},
			keyUsdWebmoney: {
				TerminalId:         "15985",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"WEBMONEY"},
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyRubWebmoney: {
				TerminalId:         "15985",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"WEBMONEY"},
			},
		},
		Type:            "ewallet",
		IsActive:        true,
		PaymentSystemId: ps4.Id,
	}

	ps5 := &billing.PaymentSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "CardPay",
		AccountingCurrency: "RUB",
		AccountingPeriod:   "every-day",
		Country:            "",
		IsActive:           true,
		Handler:            "cardpay",
	}

	pmWebMoneyWME := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "WebMoney WME",
		Group:            "WEBMONEY_WME",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "WEBMONEY",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubWebmoney: {
				TerminalId:         "15985",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"WEBMONEY"},
			},
			keyUsdWebmoney: {
				TerminalId:         "15985",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"WEBMONEY"},
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyRubWebmoney: {
				TerminalId:         "15985",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"WEBMONEY"},
			},
			keyUahWebmoney: {
				Currency:           "UAH",
				TerminalId:         "16007",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"WEBMONEY"},
			},
		},
		Type:            "ewallet",
		IsActive:        true,
		PaymentSystemId: ps5.Id,
	}
	pmBitcoin := &billing.PaymentMethod{
		Id:               primitive.NewObjectID().Hex(),
		Name:             "Bitcoin",
		Group:            "BITCOIN",
		MinPaymentAmount: 0,
		MaxPaymentAmount: 0,
		ExternalId:       "BITCOIN",
		ProductionSettings: map[string]*billing.PaymentMethodParams{
			keyRubBitcoin: {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "RUB",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN"},
			},
			keyUsdBitcoin: {
				TerminalId:         "16007",
				Secret:             "1234567890",
				SecretCallback:     "1234567890",
				Currency:           "USD",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN"},
			},
		},
		TestSettings: map[string]*billing.PaymentMethodParams{
			keyRubBitcoin: {
				Currency:           "RUB",
				TerminalId:         "16007",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN"},
			},
			keyUahBitcoin: {
				Currency:           "UAH",
				TerminalId:         "16007",
				Secret:             "A1tph4I6BD0f",
				SecretCallback:     "0V1rJ7t4jCRv",
				MccCode:            pkg.MccCodeLowRisk,
				OperatingCompanyId: suite.operatingCompany.Id,
				Brand:              []string{"BITCOIN"},
			},
		},
		Type:            "crypto",
		IsActive:        false,
		PaymentSystemId: ps5.Id,
	}

	bin := &BinData{
		Id:                 primitive.NewObjectID(),
		CardBin:            400000,
		CardBrand:          "MASTERCARD",
		CardType:           "DEBIT",
		CardCategory:       "WORLD",
		BankName:           "ALFA BANK",
		BankCountryName:    "UKRAINE",
		BankCountryIsoCode: "UA",
	}

	bin2 := &BinData{
		Id:                 primitive.NewObjectID(),
		CardBin:            408300,
		CardBrand:          "VISA",
		CardType:           "DEBIT",
		CardCategory:       "WORLD",
		BankName:           "ALFA BANK",
		BankCountryName:    "UKRAINE",
		BankCountryIsoCode: "UA",
	}

	_, err = db.Collection(collectionBinData).InsertOne(context.TODO(), bin)

	if err != nil {
		suite.FailNow("Insert BIN test data failed", "%v", err)
	}

	_, err = db.Collection(collectionBinData).InsertOne(context.TODO(), bin2)
	if err != nil {
		suite.FailNow("Insert BIN test data failed", "%v", err)
	}

	zipCode := &billing.ZipCode{
		Zip:     "98001",
		Country: "US",
		City:    "Washington",
		State: &billing.ZipCodeState{
			Code: "NJ",
			Name: "New Jersey",
		},
		CreatedAt: ptypes.TimestampNow(),
	}

	_, err = db.Collection(collectionZipCode).InsertOne(context.TODO(), zipCode)

	if err != nil {
		suite.FailNow("Insert zip codes test data failed", "%v", err)
	}

	suite.log, err = zap.NewProduction()

	if err != nil {
		suite.FailNow("Logger initialization failed", "%v", err)
	}

	broker, err := rabbitmq.NewBroker(cfg.BrokerAddress)

	if err != nil {
		suite.FailNow("Creating RabbitMQ publisher failed", "%v", err)
	}

	redisClient := database.NewRedis(
		&redis.Options{
			Addr:     cfg.RedisHost,
			Password: cfg.RedisPassword,
		},
	)

	redisdb := mocks.NewTestRedis()
	suite.cache, err = NewCacheRedis(redisdb, "cache")
	suite.service = NewBillingService(
		db,
		cfg,
		mocks.NewGeoIpServiceTestOk(),
		mocks.NewRepositoryServiceOk(),
		mocks.NewTaxServiceOkMock(),
		broker,
		redisClient,
		suite.cache,
		mocks.NewCurrencyServiceMockOk(),
		mocks.NewDocumentSignerMockOk(),
		nil,
		mocks.NewFormatterOK(),
		mocks.NewBrokerMockOk(),
		&casbinMocks.CasbinService{},
	)

	if err := suite.service.Init(); err != nil {
		suite.FailNow("Billing service initialization failed", "%v", err)
	}

	limits := []interface{}{paymentMinLimitSystem1, paymentMinLimitSystem2, paymentMinLimitSystem3, paymentMinLimitSystem4, paymentMinLimitSystem5}
	_, err = suite.service.db.Collection(collectionPaymentMinLimitSystem).InsertMany(context.TODO(), limits)
	assert.NoError(suite.T(), err)

	pms := []*billing.PaymentMethod{
		pmBankCard,
		pmQiwi,
		pmQiwiActive,
		pmBitcoin,
		pmWebMoney,
		pmWebMoneyWME,
		pmBitcoin1,
		pmBankCardNotUsed,
		pmBitcoin2,
	}
	if err := suite.service.paymentMethod.MultipleInsert(context.TODO(), pms); err != nil {
		suite.FailNow("Insert payment methods test data failed", "%v", err)
	}

	merchants := []*billing.Merchant{merchant, merchantAgreement, merchant1}
	if err := suite.service.merchant.MultipleInsert(context.TODO(), merchants); err != nil {
		suite.FailNow("Insert merchant test data failed", "%v", err)
	}

	country := []*billing.Country{ru, us, by, ua, it}
	if err := suite.service.country.MultipleInsert(context.TODO(), country); err != nil {
		suite.FailNow("Insert country test data failed", "%v", err)
	}

	if err := suite.service.project.MultipleInsert(context.TODO(), projects); err != nil {
		suite.FailNow("Insert project test data failed", "%v", err)
	}

	ps := []*billing.PaymentSystem{ps0, ps1, ps2, ps3, ps4, ps5}
	if err := suite.service.paymentSystem.MultipleInsert(context.TODO(), ps); err != nil {
		suite.FailNow("Insert payment system test data failed", "%v", err)
	}

	pgs := []*billing.PriceGroup{pgRub, pgUsd, pgCis, pgUah}
	if err := suite.service.priceGroup.MultipleInsert(context.TODO(), pgs); err != nil {
		suite.FailNow("Insert price group test data failed", "%v", err)
	}

	var productIds []string
	names := []string{"Madalin Stunt Cars M2", "Plants vs Zombies"}

	for i, n := range names {
		req := &grpc.Product{
			Object:          "product",
			Type:            "simple_product",
			Sku:             "ru_" + strconv.Itoa(i) + "_" + strcase.SnakeCase(n),
			Name:            map[string]string{"en": n},
			DefaultCurrency: "USD",
			Enabled:         true,
			Description:     map[string]string{"en": n + " description"},
			MerchantId:      projectWithProducts.MerchantId,
			ProjectId:       projectWithProducts.Id,
		}

		baseAmount := 37.00 * float64(i+1) // base amount in product's default currency

		req.Prices = append(req.Prices, &billing.ProductPrice{
			Currency: "USD",
			Region:   "USD",
			Amount:   baseAmount,
		})
		req.Prices = append(req.Prices, &billing.ProductPrice{
			Currency: "RUB",
			Region:   "RUB",
			Amount:   baseAmount * 65.13,
		})
		req.Prices = append(req.Prices, &billing.ProductPrice{
			Currency: "USD",
			Region:   "CIS",
			Amount:   baseAmount * 24.17,
		})

		prod := grpc.Product{}

		assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req, &prod))

		productIds = append(productIds, prod.Id)
	}

	var productIdsWithVirtualCurrency []string
	for i, n := range names {
		req := &grpc.Product{
			Object:          "product",
			Type:            "simple_product",
			Sku:             "test_" + strconv.Itoa(i) + "_" + strcase.SnakeCase(n),
			Name:            map[string]string{"en": n},
			DefaultCurrency: "USD",
			Enabled:         true,
			Description:     map[string]string{"en": n + " description"},
			MerchantId:      projectWithProductsInVirtualCurrency.MerchantId,
			ProjectId:       projectWithProductsInVirtualCurrency.Id,
		}

		req.Prices = append(req.Prices, &billing.ProductPrice{
			Amount:            100,
			IsVirtualCurrency: true,
		})

		prod := grpc.Product{}

		if err := suite.service.CreateOrUpdateProduct(context.TODO(), req, &prod); err != nil {
			suite.FailNow("Product create failed", "%v", err)
		}

		productIdsWithVirtualCurrency = append(productIdsWithVirtualCurrency, prod.Id)
	}

	var keyProductIds []string
	for i, n := range names {
		baseAmount := 37.00 * float64(i+1)

		req := &grpc.CreateOrUpdateKeyProductRequest{
			Object:          "key_product",
			Sku:             "ru_" + strconv.Itoa(i) + "_" + strcase.SnakeCase(n),
			Name:            map[string]string{"en": n},
			DefaultCurrency: "USD",
			Description:     map[string]string{"en": n + " description"},
			MerchantId:      projectWithKeyProducts.MerchantId,
			ProjectId:       projectWithKeyProducts.Id,
			Platforms: []*grpc.PlatformPrice{
				{
					Id: "gog",
					Prices: []*billing.ProductPrice{
						{
							Currency: "USD",
							Region:   "USD",
							Amount:   baseAmount,
						},
						{
							Currency: "RUB",
							Region:   "RUB",
							Amount:   baseAmount * 65.13,
						},
					},
				},
				{
					Id: "steam",
					Prices: []*billing.ProductPrice{
						{
							Currency: "USD",
							Region:   "USD",
							Amount:   baseAmount,
						},
						{
							Currency: "USD",
							Region:   "CIS",
							Amount:   baseAmount * 24.17,
						},
						{
							Currency: "RUB",
							Region:   "RUB",
							Amount:   baseAmount * 65.13,
						},
					},
				},
			},
		}

		res := &grpc.KeyProductResponse{}
		assert.NoError(suite.T(), suite.service.CreateOrUpdateKeyProduct(context.TODO(), req, res))
		assert.NotNil(suite.T(), res.Product)
		publishRsp := &grpc.KeyProductResponse{}
		assert.NoError(suite.T(), suite.service.PublishKeyProduct(context.TODO(), &grpc.PublishKeyProductRequest{MerchantId: projectWithKeyProducts.MerchantId, KeyProductId: res.Product.Id}, publishRsp))
		assert.EqualValuesf(suite.T(), 200, publishRsp.Status, "%s", publishRsp.Message)

		fileContent := fmt.Sprintf("%s-%s-%s-%s", RandomString(4), RandomString(4), RandomString(4), RandomString(4))
		file := []byte(fileContent)

		// Platform 1
		keysRsp := &grpc.PlatformKeysFileResponse{}
		keysReq := &grpc.PlatformKeysFileRequest{
			KeyProductId: res.Product.Id,
			PlatformId:   "steam",
			MerchantId:   projectWithKeyProducts.MerchantId,
			File:         file,
		}
		assert.NoError(suite.T(), suite.service.UploadKeysFile(context.TODO(), keysReq, keysRsp))
		assert.Equal(suite.T(), pkg.ResponseStatusOk, keysRsp.Status)

		// Platform 2
		keysRsp = &grpc.PlatformKeysFileResponse{}
		keysReq = &grpc.PlatformKeysFileRequest{
			KeyProductId: res.Product.Id,
			PlatformId:   "gog",
			MerchantId:   projectWithKeyProducts.MerchantId,
			File:         file,
		}
		assert.NoError(suite.T(), suite.service.UploadKeysFile(context.TODO(), keysReq, keysRsp))
		assert.Equal(suite.T(), pkg.ResponseStatusOk, keysRsp.Status)

		keyProductIds = append(keyProductIds, res.Product.Id)
	}

	paylinkBod, _ := ptypes.TimestampProto(now.BeginningOfDay())
	paylinkExpiresAt, _ := ptypes.TimestampProto(time.Now().Add(1 * time.Hour))
	paylinkAlreadyExpiredAt, _ := ptypes.TimestampProto(time.Now().Add(-25 * time.Hour))

	suite.paylink1 = &paylink.Paylink{
		Id:                   primitive.NewObjectID().Hex(),
		Object:               "paylink",
		Products:             productIds,
		ExpiresAt:            paylinkExpiresAt,
		CreatedAt:            paylinkBod,
		UpdatedAt:            paylinkBod,
		MerchantId:           projectWithProducts.MerchantId,
		ProjectId:            projectWithProducts.Id,
		Name:                 "Willy Wonka Strikes Back",
		IsExpired:            false,
		Visits:               0,
		NoExpiryDate:         false,
		ProductsType:         "product",
		Deleted:              false,
		TotalTransactions:    0,
		SalesCount:           0,
		ReturnsCount:         0,
		Conversion:           0,
		GrossSalesAmount:     0,
		GrossReturnsAmount:   0,
		GrossTotalAmount:     0,
		TransactionsCurrency: "",
	}

	err = suite.service.paylinkService.Insert(context.TODO(), suite.paylink1)
	assert.NoError(suite.T(), err)

	suite.paylink2 = &paylink.Paylink{
		Id:                   primitive.NewObjectID().Hex(),
		Object:               "paylink",
		Products:             productIds,
		ExpiresAt:            paylinkExpiresAt,
		CreatedAt:            paylinkBod,
		UpdatedAt:            paylinkBod,
		MerchantId:           projectWithProducts.MerchantId,
		ProjectId:            projectWithProducts.Id,
		Name:                 "Willy Wonka Strikes Back",
		IsExpired:            false,
		Visits:               0,
		NoExpiryDate:         false,
		ProductsType:         "product",
		Deleted:              true,
		TotalTransactions:    0,
		SalesCount:           0,
		ReturnsCount:         0,
		Conversion:           0,
		GrossSalesAmount:     0,
		GrossReturnsAmount:   0,
		GrossTotalAmount:     0,
		TransactionsCurrency: "",
	}

	err = suite.service.paylinkService.Insert(context.TODO(), suite.paylink2)
	assert.NoError(suite.T(), err)

	suite.paylink3 = &paylink.Paylink{
		Id:                   primitive.NewObjectID().Hex(),
		Object:               "paylink",
		Products:             productIds,
		ExpiresAt:            paylinkAlreadyExpiredAt,
		CreatedAt:            paylinkBod,
		UpdatedAt:            paylinkBod,
		MerchantId:           projectWithProducts.MerchantId,
		ProjectId:            projectWithProducts.Id,
		Name:                 "Willy Wonka Strikes Back",
		IsExpired:            true,
		Visits:               0,
		NoExpiryDate:         false,
		ProductsType:         "product",
		Deleted:              false,
		TotalTransactions:    0,
		SalesCount:           0,
		ReturnsCount:         0,
		Conversion:           0,
		GrossSalesAmount:     0,
		GrossReturnsAmount:   0,
		GrossTotalAmount:     0,
		TransactionsCurrency: "",
	}

	err = suite.service.paylinkService.Insert(context.TODO(), suite.paylink3)
	assert.NoError(suite.T(), err)

	sysCost := &billing.PaymentChannelCostSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "MASTERCARD",
		Region:             pkg.TariffRegionRussiaAndCis,
		Country:            "AZ",
		Percent:            1.5,
		FixAmount:          5,
		FixAmountCurrency:  "USD",
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}
	sysCost1 := &billing.PaymentChannelCostSystem{
		Name:               "MASTERCARD",
		Region:             pkg.TariffRegionRussiaAndCis,
		Country:            "",
		Percent:            2.2,
		FixAmount:          0,
		FixAmountCurrency:  "USD",
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}
	sysCost2 := &billing.PaymentChannelCostSystem{
		Name:               "MASTERCARD",
		Region:             pkg.TariffRegionWorldwide,
		Country:            "US",
		Percent:            2.2,
		FixAmount:          0,
		FixAmountCurrency:  "USD",
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}
	sysCost3 := &billing.PaymentChannelCostSystem{
		Name:               "Bitcoin",
		Region:             pkg.TariffRegionRussiaAndCis,
		Country:            "RU",
		Percent:            2.2,
		FixAmount:          0,
		FixAmountCurrency:  "USD",
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}

	err = suite.service.paymentChannelCostSystem.MultipleInsert(
		context.TODO(),
		[]*billing.PaymentChannelCostSystem{
			sysCost,
			sysCost1,
			sysCost2,
			sysCost3,
		},
	)

	if err != nil {
		suite.FailNow("Insert PaymentChannelCostSystem test data failed", "%v", err)
	}

	merCost := &billing.PaymentChannelCostMerchant{
		Id:                      primitive.NewObjectID().Hex(),
		MerchantId:              project.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "USD",
		MinAmount:               0.75,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "AZ",
		MethodPercent:           1.5,
		MethodFixAmount:         0.01,
		PsPercent:               3,
		PsFixedFee:              0.01,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}

	merCost1 := &billing.PaymentChannelCostMerchant{
		MerchantId:              project.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "USD",
		MinAmount:               5,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "RU",
		MethodPercent:           2.5,
		MethodFixAmount:         2,
		PsPercent:               5,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}

	merCost2 := &billing.PaymentChannelCostMerchant{
		MerchantId:              project.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "USD",
		MinAmount:               0,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "",
		MethodPercent:           2.2,
		MethodFixAmount:         0,
		PsPercent:               5,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}

	merCost3 := &billing.PaymentChannelCostMerchant{
		MerchantId:              project.GetMerchantId(),
		Name:                    "Bitcoin",
		PayoutCurrency:          "USD",
		MinAmount:               5,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "RU",
		MethodPercent:           2.5,
		MethodFixAmount:         2,
		PsPercent:               5,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}

	merCost4 := &billing.PaymentChannelCostMerchant{
		MerchantId:              project.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "USD",
		MinAmount:               5,
		Region:                  pkg.TariffRegionWorldwide,
		Country:                 "US",
		MethodPercent:           2.5,
		MethodFixAmount:         2,
		PsPercent:               5,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}
	merCost5 := &billing.PaymentChannelCostMerchant{
		Id:                      primitive.NewObjectID().Hex(),
		MerchantId:              project.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "RUB",
		MinAmount:               0.75,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "AZ",
		MethodPercent:           1.5,
		MethodFixAmount:         0.01,
		PsPercent:               3,
		PsFixedFee:              0.01,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}
	merCost6 := &billing.PaymentChannelCostMerchant{
		MerchantId:              project.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "RUB",
		MinAmount:               5,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "RU",
		MethodPercent:           2.5,
		MethodFixAmount:         2,
		PsPercent:               5,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}
	merCost7 := &billing.PaymentChannelCostMerchant{
		MerchantId:              project.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "RUB",
		MinAmount:               0,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "",
		MethodPercent:           2.2,
		MethodFixAmount:         0,
		PsPercent:               5,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}
	merCost8 := &billing.PaymentChannelCostMerchant{
		MerchantId:              project.GetMerchantId(),
		Name:                    "Bitcoin",
		PayoutCurrency:          "RUB",
		MinAmount:               5,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "RU",
		MethodPercent:           2.5,
		MethodFixAmount:         2,
		PsPercent:               5,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}
	merCost9 := &billing.PaymentChannelCostMerchant{
		MerchantId:              project.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "RUB",
		MinAmount:               5,
		Region:                  pkg.TariffRegionWorldwide,
		Country:                 "US",
		MethodPercent:           2.5,
		MethodFixAmount:         2,
		PsPercent:               5,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}

	err = suite.service.paymentChannelCostMerchant.
		MultipleInsert(context.TODO(), []*billing.PaymentChannelCostMerchant{
			merCost,
			merCost1,
			merCost2,
			merCost3,
			merCost4,
			merCost5,
			merCost6,
			merCost7,
			merCost8,
			merCost9,
		})

	if err != nil {
		suite.FailNow("Insert PaymentChannelCostMerchant test data failed", "%v", err)
	}

	suite.project = project
	suite.projectFixedAmount = projectFixedAmount
	suite.projectWithProductsInVirtualCurrency = projectWithProductsInVirtualCurrency
	suite.projectWithProducts = projectWithProducts
	suite.projectWithKeyProducts = projectWithKeyProducts
	suite.inactiveProject = inactiveProject
	suite.projectWithoutPaymentMethods = projectWithoutPaymentMethods
	suite.projectIncorrectPaymentMethodId = projectIncorrectPaymentMethodId
	suite.projectEmptyPaymentMethodTerminal = projectEmptyPaymentMethodTerminal
	suite.projectUahLimitCurrency = projectUahLimitCurrency
	suite.paymentMethod = pmBankCard
	suite.paymentMethodWithoutCommission = pmBankCardNotUsed
	suite.inactivePaymentMethod = pmBitcoin
	suite.paymentMethodWithInactivePaymentSystem = pmQiwi
	suite.paymentMethodQiwi = pmQiwiActive
	suite.pmWebMoney = pmWebMoney
	suite.pmBitcoin1 = pmBitcoin1
	suite.pmBitcoin2 = pmBitcoin2
	suite.productIds = productIds
	suite.productIdsWithVirtualCurrency = productIdsWithVirtualCurrency
	suite.merchantDefaultCurrency = "USD"
	suite.keyProductIds = keyProductIds

	paymentSysCost1 := &billing.PaymentChannelCostSystem{
		Name:              "MASTERCARD",
		Region:            pkg.TariffRegionRussiaAndCis,
		Country:           "RU",
		Percent:           0.015,
		FixAmount:         0.01,
		FixAmountCurrency: "USD",
	}

	err = suite.service.paymentChannelCostSystem.MultipleInsert(context.TODO(), []*billing.PaymentChannelCostSystem{paymentSysCost1})

	if err != nil {
		suite.FailNow("Insert PaymentChannelCostSystem test data failed", "%v", err)
	}

	paymentMerCost1 := &billing.PaymentChannelCostMerchant{
		MerchantId:              projectFixedAmount.GetMerchantId(),
		Name:                    "MASTERCARD",
		PayoutCurrency:          "USD",
		MinAmount:               0.75,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "RU",
		MethodPercent:           0.025,
		MethodFixAmount:         0.01,
		MethodFixAmountCurrency: "EUR",
		PsPercent:               0.05,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
	}
	paymentMerCost2 := &billing.PaymentChannelCostMerchant{
		MerchantId:              mocks.MerchantIdMock,
		Name:                    "MASTERCARD",
		PayoutCurrency:          "USD",
		MinAmount:               5,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "RU",
		MethodPercent:           0.025,
		MethodFixAmount:         0.02,
		MethodFixAmountCurrency: "EUR",
		PsPercent:               0.05,
		PsFixedFee:              0.05,
		PsFixedFeeCurrency:      "EUR",
	}

	err = suite.service.paymentChannelCostMerchant.MultipleInsert(context.TODO(), []*billing.PaymentChannelCostMerchant{paymentMerCost1, paymentMerCost2})

	if err != nil {
		suite.FailNow("Insert PaymentChannelCostMerchant test data failed", "%v", err)
	}

	centrifugoMock := &mocks.CentrifugoInterface{}
	centrifugoMock.On("GetChannelToken", mock.Anything, mock.Anything).Return("token")
	centrifugoMock.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	suite.service.centrifugo = centrifugoMock

	var core zapcore.Core

	lvl := zap.NewAtomicLevel()
	core, suite.zapRecorder = observer.New(lvl)
	suite.logObserver = zap.New(core)

	mbSysCost := &billing.MoneyBackCostSystem{
		Name:               "VISA",
		PayoutCurrency:     "RUB",
		UndoReason:         "chargeback",
		Region:             pkg.TariffRegionRussiaAndCis,
		Country:            "AZ",
		DaysFrom:           0,
		PaymentStage:       1,
		Percent:            3,
		FixAmount:          5,
		FixAmountCurrency:  "EUR",
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}
	mbSysCost1 := &billing.MoneyBackCostSystem{
		Name:               "VISA",
		PayoutCurrency:     "RUB",
		UndoReason:         "chargeback",
		Region:             pkg.TariffRegionRussiaAndCis,
		Country:            "RU",
		DaysFrom:           0,
		PaymentStage:       1,
		Percent:            10,
		FixAmount:          15,
		FixAmountCurrency:  "EUR",
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}
	mbSysCost2 := &billing.MoneyBackCostSystem{
		Name:               "VISA",
		PayoutCurrency:     "RUB",
		UndoReason:         "chargeback",
		Region:             pkg.TariffRegionRussiaAndCis,
		Country:            "RU",
		DaysFrom:           0,
		PaymentStage:       1,
		Percent:            10,
		FixAmount:          15,
		FixAmountCurrency:  "EUR",
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}
	mbSysCost3 := &billing.MoneyBackCostSystem{
		Name:               "MasterCard",
		PayoutCurrency:     "USD",
		UndoReason:         "reversal",
		Region:             pkg.TariffRegionRussiaAndCis,
		Country:            "RU",
		DaysFrom:           0,
		PaymentStage:       1,
		Percent:            10,
		FixAmount:          15,
		FixAmountCurrency:  "EUR",
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}

	err = suite.service.moneyBackCostSystem.MultipleInsert(context.TODO(), []*billing.MoneyBackCostSystem{mbSysCost, mbSysCost1, mbSysCost2, mbSysCost3})

	if err != nil {
		suite.FailNow("Insert MoneyBackCostSystem test data failed", "%v", err)
	}

	mbMerCost := &billing.MoneyBackCostMerchant{
		Id:                primitive.NewObjectID().Hex(),
		MerchantId:        project.GetMerchantId(),
		Name:              "VISA",
		PayoutCurrency:    "RUB",
		UndoReason:        "chargeback",
		Region:            pkg.TariffRegionRussiaAndCis,
		Country:           "AZ",
		DaysFrom:          0,
		PaymentStage:      1,
		Percent:           3,
		FixAmount:         5,
		FixAmountCurrency: "USD",
		IsPaidByMerchant:  true,
		IsActive:          true,
		MccCode:           pkg.MccCodeLowRisk,
	}
	mbMerCost1 := &billing.MoneyBackCostMerchant{
		Id:                primitive.NewObjectID().Hex(),
		MerchantId:        project.GetMerchantId(),
		Name:              "VISA",
		PayoutCurrency:    "RUB",
		UndoReason:        "chargeback",
		Region:            pkg.TariffRegionRussiaAndCis,
		Country:           "RU",
		DaysFrom:          0,
		PaymentStage:      1,
		Percent:           10,
		FixAmount:         15,
		FixAmountCurrency: "USD",
		IsPaidByMerchant:  true,
		IsActive:          true,
		MccCode:           pkg.MccCodeLowRisk,
	}
	mbMerCost2 := &billing.MoneyBackCostMerchant{
		Id:                primitive.NewObjectID().Hex(),
		MerchantId:        project.GetMerchantId(),
		Name:              "MasterCard",
		PayoutCurrency:    "USD",
		UndoReason:        "reversal",
		Region:            pkg.TariffRegionRussiaAndCis,
		Country:           "RU",
		DaysFrom:          0,
		PaymentStage:      1,
		Percent:           2,
		FixAmount:         3,
		FixAmountCurrency: "USD",
		IsPaidByMerchant:  true,
		IsActive:          true,
		MccCode:           pkg.MccCodeLowRisk,
	}

	err = suite.service.moneyBackCostMerchant.MultipleInsert(context.TODO(), []*billing.MoneyBackCostMerchant{mbMerCost, mbMerCost1, mbMerCost2})

	if err != nil {
		suite.FailNow("Insert MoneyBackCostMerchant test data failed", "%v", err)
	}
}

func (suite *OrderTestSuite) TearDownTest() {
	err := suite.service.db.Drop()

	if err != nil {
		suite.FailNow("Database deletion failed", "%v", err)
	}

	err = suite.service.db.Close()

	if err != nil {
		suite.FailNow("Database close failed", "%v", err)
	}
}

func (suite *OrderTestSuite) TestOrder_ProcessProject_Ok() {
	req := &billing.OrderCreateRequest{
		Type:      billing.OrderType_simple,
		ProjectId: suite.project.Id,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.project)

	err := processor.processProject()

	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.project)
	assert.Equal(suite.T(), processor.checked.project.Id, suite.project.Id)
}

func (suite *OrderTestSuite) TestOrder_ProcessProject_NotFound() {
	req := &billing.OrderCreateRequest{
		Type:      billing.OrderType_simple,
		ProjectId: "5bf67ebd46452d00062c7cc1",
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.project)

	err := processor.processProject()

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Equal(suite.T(), orderErrorProjectNotFound, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessProject_InactiveProject() {
	req := &billing.OrderCreateRequest{
		Type:      billing.OrderType_simple,
		ProjectId: suite.inactiveProject.Id,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.project)

	err := processor.processProject()

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Equal(suite.T(), orderErrorProjectInactive, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessCurrency_Ok() {
	req := &billing.OrderCreateRequest{
		Type:     billing.OrderType_simple,
		Currency: "RUB",
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Empty(suite.T(), processor.checked.currency)

	err := processor.processCurrency(req.Type)

	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.currency)
	assert.Equal(suite.T(), req.Currency, processor.checked.currency)
}

func (suite *OrderTestSuite) TestOrder_ProcessCurrency_Error() {
	req := &billing.OrderCreateRequest{
		Type:     billing.OrderType_simple,
		Currency: "EUR",
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Empty(suite.T(), processor.checked.currency)

	suite.service.curService = mocks.NewCurrencyServiceMockError()
	suite.service.supportedCurrencies = []string{}

	err := processor.processCurrency(req.Type)

	assert.Error(suite.T(), err)
	assert.Empty(suite.T(), processor.checked.currency)
	assert.Equal(suite.T(), orderErrorCurrencyNotFound, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPayerData_EmptyEmailAndPhone_Ok() {
	req := &billing.OrderCreateRequest{
		Type: billing.OrderType_simple,
		User: &billing.OrderUser{
			Ip: "127.0.0.1",
		},
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.user)

	err := processor.processUserData()
	assert.Nil(suite.T(), err)

	err = processor.processPayerIp()

	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.user)
	assert.NotNil(suite.T(), processor.checked.user.Address)
	assert.NotEmpty(suite.T(), processor.checked.user.Address.State)
}

func (suite *OrderTestSuite) TestOrder_ProcessPayerData_EmptySubdivision_Ok() {
	suite.service.geo = mocks.NewGeoIpServiceTestOkWithoutSubdivision()

	req := &billing.OrderCreateRequest{
		Type: billing.OrderType_simple,
		User: &billing.OrderUser{Ip: "127.0.0.1"},
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.user)

	err := processor.processUserData()
	assert.NoError(suite.T(), err)

	err = processor.processPayerIp()

	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.user)
	assert.NotNil(suite.T(), processor.checked.user.Address)
	assert.Empty(suite.T(), processor.checked.user.Address.State)

	suite.service.geo = mocks.NewGeoIpServiceTestOk()
}

func (suite *OrderTestSuite) TestOrder_ProcessPayerData_NotEmptyEmailAndPhone_Ok() {
	req := &billing.OrderCreateRequest{
		Type:      billing.OrderType_simple,
		ProjectId: suite.project.Id,
		User: &billing.OrderUser{
			Ip:    "127.0.0.1",
			Email: "some_email@unit.com",
			Phone: "123456789",
		},
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.user)

	err := processor.processProject()
	assert.NoError(suite.T(), err)

	err = processor.processUserData()
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.user)
	assert.Equal(suite.T(), req.User.Email, processor.checked.user.Email)
	assert.Equal(suite.T(), req.User.Phone, processor.checked.user.Phone)

	err = processor.processPayerIp()
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.user.Address)
}

func (suite *OrderTestSuite) TestOrder_ProcessPayerData_Error() {
	suite.service.geo = mocks.NewGeoIpServiceTestError()

	req := &billing.OrderCreateRequest{
		Type: billing.OrderType_simple,
		User: &billing.OrderUser{Ip: "127.0.0.1"},
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.user)

	err := processor.processUserData()
	assert.NoError(suite.T(), err)

	err = processor.processPayerIp()

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.user.Address)
	assert.Equal(suite.T(), orderErrorPayerRegionUnknown, err)
}

func (suite *OrderTestSuite) TestOrder_ValidateKeyProductsForOrder_Ok() {
	_, err := suite.service.GetOrderKeyProducts(context.TODO(), suite.projectWithKeyProducts.Id, suite.keyProductIds)
	assert.Nil(suite.T(), err)
}

func (suite *OrderTestSuite) TestOrder_ValidateKeyProductsForOrder_AnotherProject_Fail() {
	_, err := suite.service.GetOrderKeyProducts(context.TODO(), suite.project.Id, suite.keyProductIds)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorProductsInvalid, err)
}

func (suite *OrderTestSuite) TestOrder_ValidateProductsForOrder_Ok() {
	_, err := suite.service.GetOrderProducts(suite.projectWithProducts.Id, suite.productIds)
	assert.Nil(suite.T(), err)
}

func (suite *OrderTestSuite) TestOrder_ValidateProductsForOrder_AnotherProject_Fail() {
	_, err := suite.service.GetOrderProducts(suite.project.Id, suite.productIds)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorProductsInvalid, err)
}

func (suite *OrderTestSuite) TestOrder_ValidateProductsForOrder_OneProductIsInactive_Fail() {
	n := "Bubble Hunter"
	baseAmount := 7.00

	req := &grpc.Product{
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_3_" + strcase.SnakeCase(n),
		Name:            map[string]string{"en": n},
		DefaultCurrency: "USD",
		Enabled:         false,
		Description:     map[string]string{"en": n + " description"},
		MerchantId:      suite.projectFixedAmount.MerchantId,
		ProjectId:       suite.projectFixedAmount.Id,
		Prices: []*billing.ProductPrice{
			{
				Currency: "USD",
				Region:   "USD",
				Amount:   baseAmount,
			},
			{
				Currency: "RUB",
				Region:   "RUB",
				Amount:   baseAmount * 65.13,
			},
		},
	}

	inactiveProd := grpc.Product{}
	if assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req, &inactiveProd)) {
		products := []string{suite.productIds[0], inactiveProd.Id}
		_, err := suite.service.GetOrderProducts(suite.projectFixedAmount.Id, products)
		assert.Error(suite.T(), err)
		assert.Equal(suite.T(), orderErrorProductsInvalid, err)
	}
}

func (suite *OrderTestSuite) TestOrder_ValidateProductsForOrder_SomeProductsIsNotFound_Fail() {
	products := []string{suite.productIds[0], primitive.NewObjectID().Hex()}
	_, err := suite.service.GetOrderProducts(suite.projectFixedAmount.Id, products)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorProductsInvalid, err)
}

func (suite *OrderTestSuite) TestOrder_ValidateProductsForOrder_EmptyProducts_Fail() {
	_, err := suite.service.GetOrderProducts(suite.projectFixedAmount.Id, []string{})
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorProductsEmpty, err)
}

func (suite *OrderTestSuite) TestOrder_GetProductsOrderAmount_Ok() {
	p, err := suite.service.GetOrderProducts(suite.projectWithProducts.Id, suite.productIds)
	assert.Nil(suite.T(), err)

	amount, err := suite.service.GetOrderProductsAmount(p, &billing.PriceGroup{Currency: suite.merchantDefaultCurrency, IsActive: true})

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), amount, float64(111))
}

func (suite *OrderTestSuite) TestOrder_GetProductsOrderAmount_EmptyProducts_Fail() {
	_, err := suite.service.GetOrderProductsAmount([]*grpc.Product{}, &billing.PriceGroup{Currency: suite.merchantDefaultCurrency, IsActive: true})
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorProductsEmpty, err)
}

func (suite *OrderTestSuite) TestOrder_GetProductsOrderAmount_DifferentCurrencies_Fail() {
	n1 := "Bubble Hunter"
	baseAmount1 := 7.00
	req1 := &grpc.Product{
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_4_" + strcase.SnakeCase(n1),
		Name:            map[string]string{"en": n1},
		DefaultCurrency: "USD",
		Enabled:         false,
		Description:     map[string]string{"en": n1 + " description"},
		MerchantId:      suite.projectFixedAmount.MerchantId,
		ProjectId:       suite.projectFixedAmount.Id,
		Prices: []*billing.ProductPrice{
			{
				Currency: "USD",
				Region:   "USD",
				Amount:   baseAmount1,
			},
			{
				Currency: "RUB",
				Region:   "RUB",
				Amount:   baseAmount1 * 0.89,
			},
		},
	}
	prod1 := grpc.Product{}
	assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req1, &prod1))

	n2 := "Scary Maze"
	baseAmount2 := 8.00
	req2 := &grpc.Product{
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_5_" + strcase.SnakeCase(n2),
		Name:            map[string]string{"en": n2},
		DefaultCurrency: "USD",
		Enabled:         false,
		Description:     map[string]string{"en": n2 + " description"},
		MerchantId:      suite.projectFixedAmount.MerchantId,
		ProjectId:       suite.projectFixedAmount.Id,
		Prices: []*billing.ProductPrice{
			{
				Currency: "USD",
				Region:   "USD",
				Amount:   baseAmount2,
			},
			{
				Currency: "EUR",
				Region:   "EUR",
				Amount:   baseAmount2 * 0.89,
			},
		},
	}
	prod2 := grpc.Product{}
	assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req2, &prod2))

	p := []*grpc.Product{&prod1, &prod2}

	_, err := suite.service.GetOrderProductsAmount(p, &billing.PriceGroup{Currency: "RUB", IsActive: true})
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), grpc.ProductNoPriceInCurrencyError, err)
}

func (suite *OrderTestSuite) TestOrder_GetProductsOrderAmount_DifferentCurrenciesWithFallback_Fail() {
	n1 := "Bubble Hunter"
	baseAmount1 := 7.00
	req1 := &grpc.Product{
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_6_" + strcase.SnakeCase(n1),
		Name:            map[string]string{"en": n1},
		DefaultCurrency: "EUR",
		Enabled:         false,
		Description:     map[string]string{"en": n1 + " description"},
		MerchantId:      suite.projectFixedAmount.MerchantId,
		ProjectId:       suite.projectFixedAmount.Id,
		Prices: []*billing.ProductPrice{
			{
				Currency: "EUR",
				Region:   "EUR",
				Amount:   baseAmount1,
			},
			{
				Currency: "UAH",
				Region:   "UAH",
				Amount:   baseAmount1 * 30.21,
			},
		},
	}
	prod1 := grpc.Product{}
	assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req1, &prod1))

	n2 := "Scary Maze"
	baseAmount2 := 8.00
	req2 := &grpc.Product{
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_7_" + strcase.SnakeCase(n2),
		Name:            map[string]string{"en": n2},
		DefaultCurrency: "EUR",
		Enabled:         false,
		Description:     map[string]string{"en": n2 + " description"},
		MerchantId:      suite.projectFixedAmount.MerchantId,
		ProjectId:       suite.projectFixedAmount.Id,
		Prices: []*billing.ProductPrice{
			{
				Currency: "EUR",
				Region:   "EUR",
				Amount:   baseAmount2,
			},
			{
				Currency: "UAH",
				Region:   "UAH",
				Amount:   baseAmount2 * 30.21,
			},
		},
	}
	prod2 := grpc.Product{}
	assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req2, &prod2))

	p := []*grpc.Product{&prod1, &prod2}

	_, err := suite.service.GetOrderProductsAmount(p, &billing.PriceGroup{Currency: "RUB", IsActive: true})
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), grpc.ProductNoPriceInCurrencyError, err)
}

func (suite *OrderTestSuite) TestOrder_GetOrderProductsItems_Ok() {
	p, err := suite.service.GetOrderProducts(suite.projectWithProducts.Id, suite.productIds)
	assert.Nil(suite.T(), err)

	items, err := suite.service.GetOrderProductsItems(p, DefaultLanguage, &billing.PriceGroup{Currency: suite.merchantDefaultCurrency, IsActive: true})

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), len(items), 2)
}

func (suite *OrderTestSuite) TestOrder_GetOrderProductsItems_EmptyProducts_Fail() {
	_, err := suite.service.GetOrderProductsItems([]*grpc.Product{}, DefaultLanguage, &billing.PriceGroup{Currency: suite.merchantDefaultCurrency, IsActive: true})
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorProductsEmpty, err)
}

func (suite *OrderTestSuite) TestOrder_GetOrderProductsItems_DifferentCurrencies_Fail() {
	n1 := "Bubble Hunter"
	baseAmount1 := 7.00
	req1 := &grpc.Product{
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_8_" + strcase.SnakeCase(n1),
		Name:            map[string]string{"en": n1},
		DefaultCurrency: "USD",
		Enabled:         false,
		Description:     map[string]string{"en": n1 + " description"},
		MerchantId:      suite.projectFixedAmount.MerchantId,
		ProjectId:       suite.projectFixedAmount.Id,
		Prices: []*billing.ProductPrice{
			{
				Currency: "USD",
				Region:   "USD",
				Amount:   baseAmount1,
			},
			{
				Currency: "RUB",
				Region:   "RUB",
				Amount:   baseAmount1 * 0.89,
			},
		},
	}
	prod1 := grpc.Product{}
	assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req1, &prod1))

	n2 := "Scary Maze"
	baseAmount2 := 8.00
	req2 := &grpc.Product{
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_9_" + strcase.SnakeCase(n2),
		Name:            map[string]string{"en": n2},
		DefaultCurrency: "USD",
		Enabled:         false,
		Description:     map[string]string{"en": n2 + " description"},
		MerchantId:      suite.projectFixedAmount.MerchantId,
		ProjectId:       suite.projectFixedAmount.Id,
		Prices: []*billing.ProductPrice{
			{
				Currency: "USD",
				Region:   "USD",
				Amount:   baseAmount2,
			},
			{
				Currency: "EUR",
				Region:   "EUR",
				Amount:   baseAmount2 * 0.89,
			},
		},
	}
	prod2 := grpc.Product{}
	assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req2, &prod2))

	p := []*grpc.Product{&prod1, &prod2}

	_, err := suite.service.GetOrderProductsItems(p, DefaultLanguage, &billing.PriceGroup{Currency: "EUR", IsActive: true})
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorProductsPrice, err)
}

func (suite *OrderTestSuite) TestOrder_GetOrderProductsItems_ProductHasNoDescInSelectedLanguageButFallback_Fail() {
	n1 := "Bubble Hunter"
	baseAmount1 := 7.00
	req1 := &grpc.Product{
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_8_" + strcase.SnakeCase(n1),
		Name:            map[string]string{"en": n1},
		DefaultCurrency: "USD",
		Enabled:         false,
		Description:     map[string]string{"en": n1 + " description"},
		MerchantId:      suite.projectFixedAmount.MerchantId,
		ProjectId:       suite.projectFixedAmount.Id,
		Prices: []*billing.ProductPrice{
			{
				Currency: "USD",
				Region:   "USD",
				Amount:   baseAmount1,
			},
			{
				Currency: "RUB",
				Region:   "RUB",
				Amount:   baseAmount1 * 0.89,
			},
		},
	}
	prod1 := grpc.Product{}
	assert.NoError(suite.T(), suite.service.CreateOrUpdateProduct(context.TODO(), req1, &prod1))

	p := []*grpc.Product{&prod1}

	items, err := suite.service.GetOrderProductsItems(p, "ru", &billing.PriceGroup{Currency: suite.merchantDefaultCurrency, IsActive: true})
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), len(items), 1)
}

func (suite *OrderTestSuite) TestOrder_ProcessProjectOrderId_Ok() {
	req := &billing.OrderCreateRequest{
		Type:      billing.OrderType_simple,
		ProjectId: suite.project.Id,
		Amount:    100,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processProjectOrderId()
	assert.Nil(suite.T(), err)
}

func (suite *OrderTestSuite) TestOrder_ProcessProjectOrderId_Duplicate_Error() {
	req := &billing.OrderCreateRequest{
		Type:      billing.OrderType_simple,
		ProjectId: suite.project.Id,
		Amount:    100,
		OrderId:   "1234567890",
		Account:   "unit-test",
		Currency:  "RUB",
		Other:     make(map[string]string),
		User:      &billing.OrderUser{Ip: "127.0.0.1"},
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processUserData()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	err = processor.processPayerIp()
	assert.Nil(suite.T(), err)

	err = processor.processPaylinkProducts()
	assert.Error(suite.T(), err)

	id := primitive.NewObjectID().Hex()

	order := &billing.Order{
		Id: id,
		Project: &billing.ProjectOrder{
			Id:                processor.checked.project.Id,
			Name:              processor.checked.project.Name,
			UrlSuccess:        processor.checked.project.UrlRedirectSuccess,
			UrlFail:           processor.checked.project.UrlRedirectFail,
			SendNotifyEmail:   processor.checked.project.SendNotifyEmail,
			NotifyEmails:      processor.checked.project.NotifyEmails,
			SecretKey:         processor.checked.project.SecretKey,
			UrlCheckAccount:   processor.checked.project.UrlCheckAccount,
			UrlProcessPayment: processor.checked.project.UrlProcessPayment,
			CallbackProtocol:  processor.checked.project.CallbackProtocol,
			MerchantId:        processor.checked.project.MerchantId,
		},
		Description:    fmt.Sprintf(orderDefaultDescription, id),
		ProjectOrderId: req.OrderId,
		ProjectAccount: req.Account,
		ProjectParams:  req.Other,
		PrivateStatus:  constant.OrderStatusNew,
		CreatedAt:      ptypes.TimestampNow(),
		IsJsonRequest:  false,
	}

	_, err = suite.service.db.Collection(collectionOrder).InsertOne(context.TODO(), order)
	assert.Nil(suite.T(), err)

	err = processor.processProjectOrderId()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorProjectOrderIdIsDuplicate, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentMethod_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{
			mccCode:            pkg.MccCodeLowRisk,
			operatingCompanyId: suite.operatingCompany.Id,
		},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.paymentMethod)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentMethod_PaymentMethodInactive_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		PaymentMethod: suite.inactivePaymentMethod.Group,
		Currency:      "RUB",
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorPaymentMethodInactive, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentMethod_PaymentSystemInactive_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		PaymentMethod: suite.paymentMethodWithInactivePaymentSystem.Group,
		Currency:      "RUB",
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorPaymentSystemInactive, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessLimitAmounts_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{
			mccCode:            pkg.MccCodeLowRisk,
			operatingCompanyId: suite.operatingCompany.Id,
		},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	processor.processAmount()

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Nil(suite.T(), err)
}

func (suite *OrderTestSuite) TestOrder_ProcessLimitAmounts_ConvertAmount_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{
			mccCode:            pkg.MccCodeLowRisk,
			operatingCompanyId: suite.operatingCompany.Id,
		},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	processor.processAmount()

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Nil(suite.T(), err)
}

func (suite *OrderTestSuite) TestOrder_ProcessLimitAmounts_ProjectMinAmount_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        1,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{
			mccCode:            pkg.MccCodeLowRisk,
			operatingCompanyId: suite.operatingCompany.Id,
		},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	processor.processAmount()

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorAmountLowerThanMinAllowed, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessLimitAmounts_ProjectMaxAmount_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        10000000,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{
			mccCode:            pkg.MccCodeLowRisk,
			operatingCompanyId: suite.operatingCompany.Id,
		},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	processor.processAmount()

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorAmountGreaterThanMaxAllowed, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessLimitAmounts_PaymentMethodMinAmount_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        99,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{
			mccCode:            pkg.MccCodeLowRisk,
			operatingCompanyId: suite.operatingCompany.Id,
		},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	processor.processAmount()

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorAmountLowerThanMinAllowedPaymentMethod, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessLimitAmounts_PaymentMethodMaxAmount_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        15001,
	}
	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{
			mccCode:            pkg.MccCodeLowRisk,
			operatingCompanyId: suite.operatingCompany.Id,
		},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	processor.processAmount()

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.project, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorAmountGreaterThanMaxAllowedPaymentMethod, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessSignature_Form_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		PayerEmail:    "test@unit.unit",
	}

	req.RawParams = map[string]string{
		"PO_PROJECT_ID":     req.ProjectId,
		"PO_PAYMENT_METHOD": req.PaymentMethod,
		"PO_CURRENCY":       req.Currency,
		"PO_AMOUNT":         fmt.Sprintf("%f", req.Amount),
		"PO_ACCOUNT":        req.Account,
		"PO_DESCRIPTION":    req.Description,
		"PO_ORDER_ID":       req.OrderId,
		"PO_PAYER_EMAIL":    req.PayerEmail,
	}

	var keys []string
	var elements []string

	for k := range req.RawParams {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		value := k + "=" + req.RawParams[k]
		elements = append(elements, value)
	}

	hashString := strings.Join(elements, "") + suite.project.SecretKey

	h := sha512.New()
	h.Write([]byte(hashString))

	req.Signature = hex.EncodeToString(h.Sum(nil))

	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processSignature()
	assert.Nil(suite.T(), err)
}

func (suite *OrderTestSuite) TestOrder_ProcessSignature_Json_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		PayerEmail:    "test@unit.unit",
		IsJson:        true,
	}

	req.RawBody = `{"project":"` + suite.project.Id + `","amount":` + fmt.Sprintf("%f", req.Amount) +
		`,"currency":"` + req.Currency + `","account":"` + req.Account + `","order_id":"` + req.OrderId +
		`","description":"` + req.Description + `","payment_method":"` + req.PaymentMethod + `","payer_email":"` + req.PayerEmail +
		`","type":"` + billing.OrderType_simple + `"}`

	hashString := req.RawBody + suite.project.SecretKey

	h := sha512.New()
	h.Write([]byte(hashString))

	req.Signature = hex.EncodeToString(h.Sum(nil))

	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processSignature()
	assert.Nil(suite.T(), err)
}

func (suite *OrderTestSuite) TestOrder_ProcessSignature_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		PayerEmail:    "test@unit.unit",
		IsJson:        true,
	}

	req.RawBody = `{"project":"` + suite.project.Id + `","amount":` + fmt.Sprintf("%f", req.Amount) +
		`,"currency":"` + req.Currency + `","account":"` + req.Account + `","order_id":"` + req.OrderId +
		`","description":"` + req.Description + `","payment_method":"` + req.PaymentMethod + `","payer_email":"` + req.PayerEmail + `"}`

	fakeBody := `{"project":"` + suite.project.Id + `","amount":` + fmt.Sprintf("%f", req.Amount) +
		`,"currency":"` + req.Currency + `","account":"fake_account","order_id":"` + req.OrderId +
		`","description":"` + req.Description + `","payment_method":"` + req.PaymentMethod + `","payer_email":"` + req.PayerEmail + `"}`
	hashString := fakeBody + suite.project.SecretKey

	h := sha512.New()
	h.Write([]byte(hashString))

	req.Signature = hex.EncodeToString(h.Sum(nil))

	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}
	assert.Nil(suite.T(), processor.checked.paymentMethod)

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processSignature()
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), orderErrorSignatureInvalid, err)
}

func (suite *OrderTestSuite) TestOrder_PrepareOrder_Ok() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithProducts.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		UrlSuccess: "https://unit.test",
		UrlFail:    "https://unit.test",
		Products:   suite.productIds,
		Type:       billing.OrderType_product,
	}

	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processUserData()
	assert.Nil(suite.T(), err)

	err = processor.processPayerIp()
	assert.Nil(suite.T(), err)

	err = processor.processPayerIp()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	err = processor.processPaylinkProducts()
	assert.Nil(suite.T(), err)

	err = processor.processProjectOrderId()
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Nil(suite.T(), err)

	order, err := processor.prepareOrder()
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Equal(suite.T(), req.UrlFail, order.Project.UrlFail)
	assert.Equal(suite.T(), req.UrlSuccess, order.Project.UrlSuccess)
}

func (suite *OrderTestSuite) TestOrder_PrepareOrder_PaymentMethod_Ok() {
	req := &billing.OrderCreateRequest{
		ProjectId:     suite.projectWithProducts.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products: suite.productIds,
		Type:     billing.OrderType_product,
	}

	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{
			mccCode:            pkg.MccCodeLowRisk,
			operatingCompanyId: suite.operatingCompany.Id,
		},
	}

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processUserData()
	assert.Nil(suite.T(), err)

	err = processor.processPayerIp()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	err = processor.processPaylinkProducts()
	assert.Nil(suite.T(), err)

	err = processor.processProjectOrderId()
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Nil(suite.T(), err)

	pm, err := suite.service.paymentMethod.GetByGroupAndCurrency(context.TODO(), suite.projectWithProducts, req.PaymentMethod, processor.checked.currency)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), pm)

	err = processor.processPaymentMethod(pm)
	assert.Nil(suite.T(), err)

	order, err := processor.prepareOrder()
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), order)

	assert.NotNil(suite.T(), order.PaymentMethod)
	assert.Equal(suite.T(), processor.checked.paymentMethod.Id, order.PaymentMethod.Id)

	assert.True(suite.T(), order.Tax.Amount > 0)
	assert.NotEmpty(suite.T(), order.Tax.Currency)
}

func (suite *OrderTestSuite) TestOrder_PrepareOrder_UrlVerify_Error() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithProducts.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		UrlNotify: "https://unit.test",
		UrlVerify: "https://unit.test",
		Products:  suite.productIds,
		Type:      billing.OrderType_product,
	}

	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processUserData()
	assert.Nil(suite.T(), err)

	err = processor.processPayerIp()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	err = processor.processPaylinkProducts()
	assert.Nil(suite.T(), err)

	err = processor.processProjectOrderId()
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Nil(suite.T(), err)

	order, err := processor.prepareOrder()
	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), order)
	assert.Equal(suite.T(), orderErrorDynamicNotifyUrlsNotAllowed, err)
}

func (suite *OrderTestSuite) TestOrder_PrepareOrder_UrlRedirect_Error() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithProducts.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		UrlFail:    "https://unit.test",
		UrlSuccess: "https://unit.test",
		Products:   suite.productIds,
		Type:       billing.OrderType_product,
	}

	processor := &OrderCreateRequestProcessor{
		Service: suite.service,
		request: req,
		checked: &orderCreateRequestProcessorChecked{},
	}

	err := processor.processProject()
	assert.Nil(suite.T(), err)

	err = processor.processUserData()
	assert.Nil(suite.T(), err)

	err = processor.processPayerIp()
	assert.Nil(suite.T(), err)

	err = processor.processCurrency(req.Type)
	assert.Nil(suite.T(), err)

	err = processor.processPaylinkProducts()
	assert.Nil(suite.T(), err)

	err = processor.processProjectOrderId()
	assert.Nil(suite.T(), err)

	err = processor.processLimitAmounts()
	assert.Nil(suite.T(), err)

	processor.checked.project = suite.projectUahLimitCurrency

	order, err := processor.prepareOrder()
	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), order)
	assert.Equal(suite.T(), orderErrorDynamicRedirectUrlsNotAllowed, err)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	assert.True(suite.T(), len(rsp.Item.Id) > 0)
	assert.NotNil(suite.T(), rsp.Item.Project)
	assert.NotNil(suite.T(), rsp.Item.PaymentMethod)
	assert.Equal(suite.T(), pkg.OrderTypeOrder, rsp.Item.Type)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_ProjectInactive_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.inactiveProject.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorProjectInactive, rsp.Message)

	assert.Nil(suite.T(), rsp.Item)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_SignatureInvalid_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		PayerEmail:    "test@unit.unit",
		User: &billing.OrderUser{
			Ip: "127.0.0.1",
		},
		IsJson: true,
	}

	req.RawBody = `{"project":"` + suite.project.Id + `","amount":` + fmt.Sprintf("%f", req.Amount) +
		`,"currency":"` + req.Currency + `","account":"` + req.Account + `","order_id":"` + req.OrderId +
		`","description":"` + req.Description + `","payment_method":"` + req.PaymentMethod + `","payer_email":"` + req.PayerEmail + `"}`

	fakeBody := `{"project":"` + suite.project.Id + `","amount":` + fmt.Sprintf("%f", req.Amount) +
		`,"currency":"` + req.Currency + `","account":"fake_account","order_id":"` + req.OrderId +
		`","description":"` + req.Description + `","payment_method":"` + req.PaymentMethod + `","payer_email":"` + req.PayerEmail + `"}`
	hashString := fakeBody + suite.project.SecretKey

	h := sha512.New()
	h.Write([]byte(hashString))

	req.Signature = hex.EncodeToString(h.Sum(nil))

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorSignatureInvalid, rsp.Message)

	assert.Nil(suite.T(), rsp.Item)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_Error_CheckoutWithoutAmount() {
	suite.project.IsProductsCheckout = true
	assert.NoError(suite.T(), suite.service.project.Update(context.TODO(), suite.project))

	req := &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "USD",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_product,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorCheckoutWithoutProducts, rsp.Message)

	suite.project.IsProductsCheckout = false
	assert.NoError(suite.T(), suite.service.project.Update(context.TODO(), suite.project))

	req = &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "USD",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_key,
	}

	rsp = &grpc.OrderCreateProcessResponse{}
	err = suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorCheckoutWithoutProducts, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_Error_CheckoutWithoutProducts() {
	suite.project.IsProductsCheckout = false
	assert.NoError(suite.T(), suite.service.project.Update(context.TODO(), suite.project))

	req := &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "USD",
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorCheckoutWithoutAmount, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_CurrencyInvalid_Error() {
	req := &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "AUD",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorCurrencyNotFound, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_CurrencyEmpty_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.projectEmptyPaymentMethodTerminal.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorCurrencyIsRequired, rsp.Message)

	assert.Nil(suite.T(), rsp.Item)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_DuplicateProjectOrderId_Error() {
	orderId := primitive.NewObjectID().Hex()

	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       orderId,
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	order := &billing.Order{
		Id: primitive.NewObjectID().Hex(),
		Project: &billing.ProjectOrder{
			Id:                suite.project.Id,
			Name:              suite.project.Name,
			UrlSuccess:        suite.project.UrlRedirectSuccess,
			UrlFail:           suite.project.UrlRedirectFail,
			SendNotifyEmail:   suite.project.SendNotifyEmail,
			NotifyEmails:      suite.project.NotifyEmails,
			SecretKey:         suite.project.SecretKey,
			UrlCheckAccount:   suite.project.UrlCheckAccount,
			UrlProcessPayment: suite.project.UrlProcessPayment,
			CallbackProtocol:  suite.project.CallbackProtocol,
			MerchantId:        suite.project.MerchantId,
		},
		Description:    fmt.Sprintf(orderDefaultDescription, orderId),
		ProjectOrderId: req.OrderId,
		ProjectAccount: req.Account,
		ProjectParams:  req.Other,
		PrivateStatus:  constant.OrderStatusNew,
		CreatedAt:      ptypes.TimestampNow(),
		IsJsonRequest:  false,
	}

	_, err := suite.service.db.Collection(collectionOrder).InsertOne(context.TODO(), order)
	assert.Nil(suite.T(), err)

	rsp := &grpc.OrderCreateProcessResponse{}
	err = suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorProjectOrderIdIsDuplicate, rsp.Message)

	assert.Nil(suite.T(), rsp.Item)
	assert.Equal(suite.T(), orderErrorProjectOrderIdIsDuplicate, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_PaymentMethodInvalid_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.inactivePaymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorPaymentMethodInactive, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcess_AmountInvalid_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        10,
		Account:       "unit test",
		Description:   "unit test",
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorAmountLowerThanMinAllowed, rsp.Message)

	assert.Nil(suite.T(), rsp.Item)
}

func (suite *OrderTestSuite) TestOrder_ProcessRenderFormPaymentMethods_DevEnvironment_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	order := rsp.Item
	assert.True(suite.T(), len(order.Id) > 0)

	processor := &PaymentFormProcessor{
		service: suite.service,
		order:   order,
		request: &grpc.PaymentFormJsonDataRequest{
			OrderId: order.Id,
			Scheme:  "http",
			Host:    "unit.test",
		},
	}

	pms, err := processor.processRenderFormPaymentMethods(context.TODO(), suite.project)

	assert.Nil(suite.T(), err)
	assert.True(suite.T(), len(pms) > 0)
}

func (suite *OrderTestSuite) TestOrder_ProcessRenderFormPaymentMethods_ProdEnvironment_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	order := rsp.Item
	assert.True(suite.T(), len(order.Id) > 0)

	processor := &PaymentFormProcessor{
		service: suite.service,
		order:   order,
		request: &grpc.PaymentFormJsonDataRequest{
			OrderId: order.Id,
			Scheme:  "http",
			Host:    "unit.test",
		},
	}
	pms, err := processor.processRenderFormPaymentMethods(context.TODO(), suite.project)

	assert.Nil(suite.T(), err)
	assert.True(suite.T(), len(pms) > 0)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentMethodsData_SavedCards_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	order := rsp.Item

	processor := &PaymentFormProcessor{service: suite.service, order: order}

	pm := &billing.PaymentFormPaymentMethod{
		Id:            suite.paymentMethod.Id,
		Name:          suite.paymentMethod.Name,
		Type:          suite.paymentMethod.Type,
		Group:         suite.paymentMethod.Group,
		AccountRegexp: suite.paymentMethod.AccountRegexp,
	}

	assert.True(suite.T(), len(pm.SavedCards) <= 0)

	err = processor.processPaymentMethodsData(pm)
	assert.Nil(suite.T(), err)
	assert.True(suite.T(), pm.HasSavedCards)
	assert.True(suite.T(), len(pm.SavedCards) > 0)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentMethodsData_EmptySavedCards_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	suite.service.rep = mocks.NewRepositoryServiceEmpty()

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	order := rsp.Item

	processor := &PaymentFormProcessor{service: suite.service, order: order}

	pm := &billing.PaymentFormPaymentMethod{
		Id:            suite.paymentMethod.Id,
		Name:          suite.paymentMethod.Name,
		Type:          suite.paymentMethod.Type,
		Group:         suite.paymentMethod.Group,
		AccountRegexp: suite.paymentMethod.AccountRegexp,
	}

	assert.True(suite.T(), len(pm.SavedCards) <= 0)

	err = processor.processPaymentMethodsData(pm)
	assert.Nil(suite.T(), err)
	assert.False(suite.T(), pm.HasSavedCards)
	assert.Len(suite.T(), pm.SavedCards, 0)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentMethodsData_NotBankCard_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	suite.service.rep = mocks.NewRepositoryServiceEmpty()

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	order := rsp.Item

	processor := &PaymentFormProcessor{service: suite.service, order: order}

	pm := &billing.PaymentFormPaymentMethod{
		Id:            suite.paymentMethod.Id,
		Name:          suite.paymentMethodWithInactivePaymentSystem.Name,
		Type:          suite.paymentMethodWithInactivePaymentSystem.Type,
		Group:         suite.paymentMethodWithInactivePaymentSystem.Group,
		AccountRegexp: suite.paymentMethodWithInactivePaymentSystem.AccountRegexp,
	}

	assert.True(suite.T(), len(pm.SavedCards) <= 0)

	err = processor.processPaymentMethodsData(pm)
	assert.Nil(suite.T(), err)
	assert.False(suite.T(), pm.HasSavedCards)
	assert.Len(suite.T(), pm.SavedCards, 0)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentMethodsData_GetSavedCards_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	suite.service.rep = mocks.NewRepositoryServiceError()

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	order := rsp.Item

	processor := &PaymentFormProcessor{service: suite.service, order: order}

	pm := &billing.PaymentFormPaymentMethod{
		Id:            suite.paymentMethod.Id,
		Name:          suite.paymentMethod.Name,
		Type:          suite.paymentMethod.Type,
		Group:         suite.paymentMethod.Group,
		AccountRegexp: suite.paymentMethod.AccountRegexp,
	}

	err = processor.processPaymentMethodsData(pm)
	assert.Nil(suite.T(), err)
	assert.False(suite.T(), pm.HasSavedCards)
	assert.Len(suite.T(), pm.SavedCards, 0)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcessAlreadyProcessed_Error() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
			Address: &billing.OrderBillingAddress{
				Country: "RU",
			},
			Locale: "ru-RU",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)

	order := rsp1.Item
	order.PrivateStatus = constant.OrderStatusPaymentSystemCanceled
	assert.NoError(suite.T(), suite.service.updateOrder(context.TODO(), order))

	req1 := &grpc.PaymentFormJsonDataRequest{OrderId: order.Uuid, Scheme: "https", Host: "unit.test",
		Ip: "127.0.0.1",
	}

	rsp2 := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp2)
	assert.Nil(suite.T(), err)
	assert.EqualValues(suite.T(), 400, rsp2.Status)
	assert.Equal(suite.T(), orderErrorDontHaveReceiptUrl, rsp2.Message)

	order.ReceiptUrl = "http://test.test"
	assert.NoError(suite.T(), suite.service.updateOrder(context.TODO(), order))

	rsp2 = &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp2)
	assert.Nil(suite.T(), err)
	assert.EqualValues(suite.T(), 200, rsp2.Status)
	assert.Equal(suite.T(), billing.OrderType_simple, rsp2.Item.Type)
	assert.True(suite.T(), rsp2.Item.IsAlreadyProcessed)
	assert.EqualValues(suite.T(), order.ReceiptUrl, rsp2.Item.ReceiptUrl)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email:  "test@unit.unit",
			Ip:     "127.0.0.1",
			Locale: "ru-RU",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item
	assert.NotNil(suite.T(), order.CountryRestriction)
	assert.Equal(suite.T(), order.CountryRestriction.IsoCodeA2, "RU")
	assert.True(suite.T(), order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), order.CountryRestriction.ChangeAllowed)
	assert.False(suite.T(), order.UserAddressDataRequired)
	assert.Equal(suite.T(), order.PrivateStatus, int32(constant.OrderStatusNew))

	req1 := &grpc.PaymentFormJsonDataRequest{OrderId: order.Uuid, Scheme: "https", Host: "unit.test",
		Ip: "94.131.198.60", // Ukrainian IP -> payments not allowed but available to change country
	}
	rsp := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), billing.OrderType_simple, rsp.Item.Type)
	assert.True(suite.T(), len(rsp.Item.PaymentMethods) > 0)
	assert.True(suite.T(), len(rsp.Item.PaymentMethods[0].Id) > 0)
	assert.Equal(suite.T(), len(rsp.Item.Items), 0)
	assert.Equal(suite.T(), req.Description, rsp.Item.Description)
	assert.True(suite.T(), rsp.Item.CountryPaymentsAllowed)
	assert.True(suite.T(), rsp.Item.CountryChangeAllowed)
	assert.Equal(suite.T(), req.User.Locale, rsp.Item.Lang)
	assert.NotNil(suite.T(), rsp.Item.Project)
	assert.NotZero(suite.T(), rsp.Item.Project.Id)

	order, err = suite.service.getOrderByUuid(context.TODO(), order.Uuid)
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), order.CountryRestriction)
	assert.Equal(suite.T(), order.CountryRestriction.IsoCodeA2, "RU")
	assert.True(suite.T(), order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), order.CountryRestriction.ChangeAllowed)
	assert.False(suite.T(), order.UserAddressDataRequired)
	assert.Equal(suite.T(), rsp.Item.Project.Id, order.Project.Id)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcessWithProducts_Ok() {
	req := &billing.OrderCreateRequest{
		ProjectId:     suite.projectWithProducts.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products: suite.productIds,
		Type:     billing.OrderType_product,
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	req1 := &grpc.PaymentFormJsonDataRequest{OrderId: order.Uuid, Scheme: "https", Host: "unit.test"}
	rsp := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), billing.OrderType_product, rsp.Item.Type)
	assert.True(suite.T(), len(rsp.Item.PaymentMethods) > 0)
	assert.True(suite.T(), len(rsp.Item.PaymentMethods[0].Id) > 0)
	assert.Equal(suite.T(), len(rsp.Item.Items), 2)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_BankCard_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldPan:             "4000000000000002",
		pkg.PaymentCreateFieldCvv:             "123",
		pkg.PaymentCreateFieldMonth:           "02",
		pkg.PaymentCreateFieldYear:            "2100",
		pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.order)
	assert.NotNil(suite.T(), processor.checked.project)
	assert.NotNil(suite.T(), processor.checked.paymentMethod)

	bankBrand, ok := processor.checked.order.PaymentRequisites[pkg.PaymentCreateBankCardFieldBrand]

	assert.True(suite.T(), ok)
	assert.True(suite.T(), len(bankBrand) > 0)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_Bitcoin_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin1.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.order)
	assert.NotNil(suite.T(), processor.checked.project)
	assert.NotNil(suite.T(), processor.checked.paymentMethod)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_OrderIdEmpty_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)

	data := map[string]string{
		pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin1.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorCreatePaymentRequiredFieldIdNotFound, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_PaymentMethodEmpty_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId: rsp.Id,
		pkg.PaymentCreateFieldEmail:   "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:  "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorCreatePaymentRequiredFieldPaymentMethodNotFound, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_EmailEmpty_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin1.Id,
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorCreatePaymentRequiredFieldEmailNotFound, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_OrderNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         primitive.NewObjectID().Hex(),
		pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin1.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorNotFound, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_OrderHasEndedStatus_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	rsp.PrivateStatus = constant.OrderStatusProjectComplete
	err = suite.service.updateOrder(context.TODO(), rsp)

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin1.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorOrderAlreadyComplete, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_ProjectProcess_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	rsp.Project.Id = suite.inactiveProject.Id
	err = suite.service.updateOrder(context.TODO(), rsp)

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin1.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorProjectInactive, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_PaymentMethodNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: primitive.NewObjectID().Hex(),
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorPaymentMethodNotFound, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_PaymentMethodProcess_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.inactivePaymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorPaymentMethodInactive, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_AmountLimitProcess_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	rsp.OrderAmount = 10
	err = suite.service.updateOrder(context.TODO(), rsp)

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), orderErrorAmountLowerThanMinAllowed, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_BankCardNumberInvalid_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldPan:             "fake_bank_card_number",
		pkg.PaymentCreateFieldCvv:             "123",
		pkg.PaymentCreateFieldMonth:           "02",
		pkg.PaymentCreateFieldYear:            "2100",
		pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), bankCardPanIsInvalid, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_GetBinData_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldPan:             "5555555555554444",
		pkg.PaymentCreateFieldCvv:             "123",
		pkg.PaymentCreateFieldMonth:           "02",
		pkg.PaymentCreateFieldYear:            "2100",
		pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
	}

	suite.service.rep = mocks.NewRepositoryServiceError()

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.order)
	assert.NotNil(suite.T(), processor.checked.project)
	assert.NotNil(suite.T(), processor.checked.paymentMethod)

	bankBrand, ok := processor.checked.order.PaymentRequisites[pkg.PaymentCreateBankCardFieldBrand]

	assert.False(suite.T(), ok)
	assert.Len(suite.T(), bankBrand, 0)

	suite.service.rep = mocks.NewRepositoryServiceOk()
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_AccountEmpty_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin1.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldCrypto:          "",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), processor.checked.order)
	assert.Nil(suite.T(), processor.checked.project)
	assert.Nil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), paymentSystemErrorEWalletIdentifierIsInvalid, err)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_ChangeProjectAccount_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	rsp := rsp1.Item
	assert.Equal(suite.T(), "", rsp.ProjectAccount)

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldPan:             "4000000000000002",
		pkg.PaymentCreateFieldCvv:             "123",
		pkg.PaymentCreateFieldMonth:           "02",
		pkg.PaymentCreateFieldYear:            "2100",
		pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())

	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.order)
	assert.NotNil(suite.T(), processor.checked.project)
	assert.NotNil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), "test@unit.unit", processor.checked.order.User.Email)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	expireYear := time.Now().AddDate(1, 0, 0)

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         order.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
		Ip: "127.0.0.1",
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.True(suite.T(), len(rsp.RedirectUrl) > 0)
	assert.Nil(suite.T(), rsp.Message)
	assert.True(suite.T(), rsp.NeedRedirect)

	oid, err := primitive.ObjectIDFromHex(order.Id)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var order1 *billing.Order
	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order1)
	assert.NotNil(suite.T(), order1)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_ProcessValidation_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         order.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Len(suite.T(), rsp.RedirectUrl, 0)
	assert.True(suite.T(), len(rsp.Message.Message) > 0)
	assert.Equal(suite.T(), bankCardExpireYearIsRequired, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_ChangeTerminalData_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	expireYear := time.Now().AddDate(1, 0, 0)

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         order.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
		Ip: "127.0.0.1",
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.True(suite.T(), len(rsp.RedirectUrl) > 0)
	assert.Nil(suite.T(), rsp.Message)
	assert.True(suite.T(), rsp.NeedRedirect)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_CreatePaymentSystemHandler_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         order.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin1.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldCrypto:          "bitcoin_address",
		},
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusSystemError, rsp.Status)
	assert.Len(suite.T(), rsp.RedirectUrl, 0)
	assert.True(suite.T(), len(rsp.Message.Message) > 0)
	assert.Equal(suite.T(), paymentSystemErrorHandlerNotFound.Error(), rsp.Message.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_FormInputTimeExpired_Error() {
	req1 := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req1, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	rsp1 := rsp.Item

	oid, err := primitive.ObjectIDFromHex(rsp1.Id)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var order *billing.Order
	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order)
	assert.NotNil(suite.T(), order)

	order.ExpireDateToFormInput, err = ptypes.TimestampProto(time.Now().Add(time.Minute * -40))
	assert.NoError(suite.T(), err)

	err = suite.service.updateOrder(context.TODO(), order)

	expireYear := time.Now().AddDate(1, 0, 0)

	req2 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp1.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
	}

	rsp2 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req2, rsp2)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp2.Status)
	assert.Equal(suite.T(), orderErrorFormInputTimeExpired, rsp2.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentCallbackProcess_Ok() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithProducts.Id,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		Products:    suite.productIds,
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_product,
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	expireYear := time.Now().AddDate(1, 0, 0)

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         order.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
		Ip: "127.0.0.1",
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)

	oid, err := primitive.ObjectIDFromHex(order.Id)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var order1 *billing.Order
	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order1)

	callbackRequest := &billing.CardPayPaymentCallback{
		PaymentMethod: suite.paymentMethod.ExternalId,
		CallbackTime:  time.Now().Format("2006-01-02T15:04:05Z"),
		MerchantOrder: &billing.CardPayMerchantOrder{
			Id:          order.Id,
			Description: order.Description,
			Items: []*billing.CardPayItem{
				{
					Name:        order.Items[0].Name,
					Description: order.Items[0].Name,
					Count:       1,
					Price:       order.Items[0].Amount,
				},
			},
		},
		CardAccount: &billing.CallbackCardPayBankCardAccount{
			Holder:             order.PaymentRequisites[pkg.PaymentCreateFieldHolder],
			IssuingCountryCode: "RU",
			MaskedPan:          order.PaymentRequisites[pkg.PaymentCreateFieldPan],
			Token:              primitive.NewObjectID().Hex(),
		},
		Customer: &billing.CardPayCustomer{
			Email:  order.User.Email,
			Ip:     order.User.Ip,
			Id:     order.ProjectAccount,
			Locale: "Europe/Moscow",
		},
		PaymentData: &billing.CallbackCardPayPaymentData{
			Id:          primitive.NewObjectID().Hex(),
			Amount:      order1.TotalPaymentAmount,
			Currency:    order1.Currency,
			Description: order.Description,
			Is_3D:       true,
			Rrn:         primitive.NewObjectID().Hex(),
			Status:      pkg.CardPayPaymentResponseStatusCompleted,
		},
	}

	buf, err := json.Marshal(callbackRequest)
	assert.Nil(suite.T(), err)

	hash := sha512.New()
	hash.Write([]byte(string(buf) + order1.PaymentMethod.Params.SecretCallback))

	callbackData := &grpc.PaymentNotifyRequest{
		OrderId:   order.Id,
		Request:   buf,
		Signature: hex.EncodeToString(hash.Sum(nil)),
	}

	callbackResponse := &grpc.PaymentNotifyResponse{}
	err = suite.service.PaymentCallbackProcess(context.TODO(), callbackData, callbackResponse)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.StatusOK, callbackResponse.Status)

	oid, err = primitive.ObjectIDFromHex(order.Id)
	assert.NoError(suite.T(), err)
	filter = bson.M{"_id": oid}

	var order2 *billing.Order
	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order2)
	suite.NotNil(suite.T(), order2)

	assert.Equal(suite.T(), int32(constant.OrderStatusPaymentSystemComplete), order2.PrivateStatus)
	assert.Equal(suite.T(), callbackRequest.GetId(), order2.Transaction)
	assert.NotNil(suite.T(), order2.PaymentMethod.Card)
	assert.Equal(suite.T(), order2.PaymentMethod.Card.Brand, "MASTERCARD")
	assert.Equal(suite.T(), order2.PaymentMethod.Card.Masked, "400000******0002")
	assert.Equal(suite.T(), order2.PaymentMethod.Card.First6, "400000")
	assert.Equal(suite.T(), order2.PaymentMethod.Card.Last4, "0002")
	assert.Equal(suite.T(), order2.PaymentMethod.Card.ExpiryMonth, "02")
	assert.Equal(suite.T(), order2.PaymentMethod.Card.ExpiryYear, expireYear.Format("2006"))
	assert.Equal(suite.T(), order2.PaymentMethod.Card.Secure3D, true)
	assert.NotEmpty(suite.T(), order2.PaymentMethod.Card.Fingerprint)
}

func (suite *OrderTestSuite) TestOrder_PaymentCallbackProcess_Recurring_Ok() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithProducts.Id,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products: suite.productIds,
		Type:     billing.OrderType_product,
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	expireYear := time.Now().AddDate(1, 0, 0)

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         order.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
			pkg.PaymentCreateFieldStoreData:       "1",
		},
		Ip: "127.0.0.1",
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)

	oid, err := primitive.ObjectIDFromHex(order.Id)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var order1 *billing.Order
	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order1)
	suite.NotNil(suite.T(), order1)

	callbackRequest := &billing.CardPayPaymentCallback{
		PaymentMethod: suite.paymentMethod.ExternalId,
		CallbackTime:  time.Now().Format("2006-01-02T15:04:05Z"),
		MerchantOrder: &billing.CardPayMerchantOrder{
			Id:          order.Id,
			Description: order.Description,
			Items: []*billing.CardPayItem{
				{
					Name:        order.Items[0].Name,
					Description: order.Items[0].Name,
					Count:       1,
					Price:       order.Items[0].Amount,
				},
			},
		},
		CardAccount: &billing.CallbackCardPayBankCardAccount{
			Holder:             order.PaymentRequisites[pkg.PaymentCreateFieldHolder],
			IssuingCountryCode: "RU",
			MaskedPan:          order.PaymentRequisites[pkg.PaymentCreateFieldPan],
			Token:              primitive.NewObjectID().Hex(),
		},
		Customer: &billing.CardPayCustomer{
			Email:  order.User.Email,
			Ip:     order.User.Ip,
			Id:     order.ProjectAccount,
			Locale: "Europe/Moscow",
		},
		RecurringData: &billing.CardPayCallbackRecurringData{
			Id:          primitive.NewObjectID().Hex(),
			Amount:      order1.TotalPaymentAmount,
			Currency:    order1.Currency,
			Description: order.Description,
			Is_3D:       true,
			Rrn:         primitive.NewObjectID().Hex(),
			Status:      pkg.CardPayPaymentResponseStatusCompleted,
			Filing: &billing.CardPayCallbackRecurringDataFilling{
				Id: primitive.NewObjectID().Hex(),
			},
		},
	}

	buf, err := json.Marshal(callbackRequest)
	assert.Nil(suite.T(), err)

	hash := sha512.New()
	hash.Write([]byte(string(buf) + order1.PaymentMethod.Params.SecretCallback))

	callbackData := &grpc.PaymentNotifyRequest{
		OrderId:   order.Id,
		Request:   buf,
		Signature: hex.EncodeToString(hash.Sum(nil)),
	}

	callbackResponse := &grpc.PaymentNotifyResponse{}
	err = suite.service.PaymentCallbackProcess(context.TODO(), callbackData, callbackResponse)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.StatusOK, callbackResponse.Status)

	var order2 *billing.Order
	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order2)
	suite.NotNil(suite.T(), order2)

	assert.Equal(suite.T(), int32(constant.OrderStatusPaymentSystemComplete), order2.PrivateStatus)
	assert.Equal(suite.T(), callbackRequest.GetId(), order2.Transaction)
	assert.Equal(suite.T(), callbackRequest.GetAmount(), order2.TotalPaymentAmount)
	assert.Equal(suite.T(), callbackRequest.GetCurrency(), order2.Currency)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormLanguageChanged_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangeLangRequest{
		OrderId: rsp.Uuid,
		Lang:    "en",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormLanguageChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotNil(suite.T(), rsp1.Item)
	assert.False(suite.T(), rsp1.Item.UserAddressDataRequired)
	assert.Equal(suite.T(), rsp.User.Address.Country, rsp1.Item.UserIpData.Country)
	assert.Equal(suite.T(), rsp.User.Address.PostalCode, rsp1.Item.UserIpData.Zip)
	assert.Equal(suite.T(), rsp.User.Address.City, rsp1.Item.UserIpData.City)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormLanguageChanged_OrderNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangeLangRequest{
		OrderId: uuid.New().String(),
		Lang:    "en",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormLanguageChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorNotFound, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormLanguageChanged_NoChanges_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email:  "test@unit.unit",
			Ip:     "127.0.0.1",
			Locale: "en",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req2 := &grpc.PaymentFormJsonDataRequest{
		OrderId: rsp.Uuid,
		Scheme:  "http",
		Host:    "localhost",
		Locale:  "en-US",
		Ip:      "127.0.0.1",
	}
	rsp2 := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req2, rsp2)
	assert.NoError(suite.T(), err)

	req1 := &grpc.PaymentFormUserChangeLangRequest{
		OrderId: rsp.Uuid,
		Lang:    "en",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormLanguageChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotNil(suite.T(), rsp1.Item)
	assert.False(suite.T(), rsp1.Item.UserAddressDataRequired)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_BankCard_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.paymentMethod.Id,
		Account:  "4000000000000002",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotNil(suite.T(), rsp1.Item)
	assert.False(suite.T(), rsp1.Item.UserAddressDataRequired)
	assert.Equal(suite.T(), "RU", rsp1.Item.UserIpData.Country)
	assert.Equal(suite.T(), rsp.User.Address.PostalCode, rsp1.Item.UserIpData.Zip)
	assert.Equal(suite.T(), rsp.User.Address.City, rsp1.Item.UserIpData.City)
	assert.Equal(suite.T(), "MASTERCARD", rsp1.Item.Brand)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_Qiwi_Ok() {
	sysCost := &billing.PaymentChannelCostSystem{
		Id:                 primitive.NewObjectID().Hex(),
		Name:               "QIWI",
		Region:             pkg.TariffRegionRussiaAndCis,
		Country:            "RU",
		Percent:            1.5,
		FixAmount:          5,
		FixAmountCurrency:  "USD",
		CreatedAt:          nil,
		UpdatedAt:          nil,
		IsActive:           true,
		MccCode:            pkg.MccCodeLowRisk,
		OperatingCompanyId: suite.operatingCompany.Id,
	}

	err := suite.service.paymentChannelCostSystem.Insert(context.TODO(), sysCost)
	assert.NoError(suite.T(), err)

	merCost := &billing.PaymentChannelCostMerchant{
		Id:                      primitive.NewObjectID().Hex(),
		MerchantId:              suite.project.MerchantId,
		Name:                    "QIWI",
		PayoutCurrency:          "USD",
		MinAmount:               0.75,
		Region:                  pkg.TariffRegionRussiaAndCis,
		Country:                 "RU",
		MethodPercent:           1.5,
		MethodFixAmount:         0.01,
		PsPercent:               3,
		PsFixedFee:              0.01,
		PsFixedFeeCurrency:      "EUR",
		MethodFixAmountCurrency: "USD",
		IsActive:                true,
		MccCode:                 pkg.MccCodeLowRisk,
	}
	err = suite.service.paymentChannelCostMerchant.Insert(context.TODO(), merCost)
	assert.NoError(suite.T(), err)

	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err = suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.paymentMethodQiwi.Id,
		Account:  "380123456789",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotNil(suite.T(), rsp1.Item)
	assert.False(suite.T(), rsp1.Item.UserAddressDataRequired)
	assert.Equal(suite.T(), "RU", rsp1.Item.UserIpData.Country)
	assert.Equal(suite.T(), rsp.User.Address.PostalCode, rsp1.Item.UserIpData.Zip)
	assert.Equal(suite.T(), rsp.User.Address.City, rsp1.Item.UserIpData.City)
	assert.Empty(suite.T(), rsp1.Item.Brand)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_Qiwi_SystemCostNotFound() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.paymentMethodQiwi.Id,
		Account:  "380123456789",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), rsp1.Message, orderErrorCostsRatesNotFound)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_OrderNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  uuid.New().String(),
		MethodId: suite.paymentMethod.Id,
		Account:  "4000000000000002",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorNotFound, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_PaymentMethodNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: primitive.NewObjectID().Hex(),
		Account:  "4000000000000002",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorPaymentMethodNotFound, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_AccountIncorrect_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.paymentMethod.Id,
		Account:  "some_account",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorPaymentAccountIncorrect, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_BinDataNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.paymentMethod.Id,
		Account:  "5555555555554444",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorCountryByPaymentAccountNotFound, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_QiwiAccountIncorrect_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.paymentMethodQiwi.Id,
		Account:  "some_account",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorPaymentAccountIncorrect, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_QiwiAccountCountryNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.paymentMethodQiwi.Id,
		Account:  "244636739467",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorCountryByPaymentAccountNotFound, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_Bitcoin_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.pmBitcoin1.Id,
		Account:  "some_account",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotNil(suite.T(), rsp1.Item)
	assert.False(suite.T(), rsp1.Item.UserAddressDataRequired)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormPaymentAccountChanged_NoChanges_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.PaymentFormUserChangePaymentAccountRequest{
		OrderId:  rsp.Uuid,
		MethodId: suite.paymentMethodWithInactivePaymentSystem.Id,
		Account:  "79211234567",
	}
	rsp1 := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPaymentAccountChanged(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), rsp1.Message, orderErrorPaymentSystemInactive)
	assert.Nil(suite.T(), rsp1.Item)
}

func (suite *OrderTestSuite) TestOrder_OrderReCalculateAmounts_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	order, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.Nil(suite.T(), order.BillingAddress)

	req1 := &grpc.ProcessBillingAddressRequest{
		OrderId: rsp.Uuid,
		Country: "US",
		Zip:     "98001",
	}
	rsp1 := &grpc.ProcessBillingAddressResponse{}
	err = suite.service.ProcessBillingAddress(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotNil(suite.T(), rsp1.Item)
	assert.True(suite.T(), rsp1.Item.HasVat)
	assert.True(suite.T(), rsp1.Item.Vat > 0)
	assert.True(suite.T(), rsp1.Item.Amount > 0)
	assert.True(suite.T(), rsp1.Item.TotalAmount > 0)

	assert.NotEqual(suite.T(), order.Tax.Amount, rsp1.Item.Vat)
	assert.NotEqual(suite.T(), float32(order.TotalPaymentAmount), rsp1.Item.TotalAmount)

	order1, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order1.BillingAddress)

	assert.Equal(suite.T(), order1.Tax.Amount, rsp1.Item.Vat)
	assert.Equal(suite.T(), order1.TotalPaymentAmount, rsp1.Item.TotalAmount)
	assert.Equal(suite.T(), order1.Currency, rsp1.Item.Currency)
	assert.Equal(suite.T(), order1.Items, rsp1.Item.Items)
}

func (suite *OrderTestSuite) TestOrder_OrderReCalculateAmounts_OrderNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.True(suite.T(), len(rsp.Id) > 0)

	req1 := &grpc.ProcessBillingAddressRequest{
		OrderId: uuid.New().String(),
		Country: "US",
		Zip:     "98001",
	}
	rsp1 := &grpc.ProcessBillingAddressResponse{}
	err = suite.service.ProcessBillingAddress(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorNotFound, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_UserAddressDataRequired_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	order, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Nil(suite.T(), order.BillingAddress)

	order.UserAddressDataRequired = true
	err = suite.service.updateOrder(context.TODO(), order)
	assert.NoError(suite.T(), err)

	expireYear := time.Now().AddDate(1, 0, 0)

	req1 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
			pkg.PaymentCreateFieldUserCountry:     "US",
			pkg.PaymentCreateFieldUserCity:        "Washington",
			pkg.PaymentCreateFieldUserZip:         "98001",
		},
		Ip: "127.0.0.1",
	}

	rsp1 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req1, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.True(suite.T(), len(rsp1.RedirectUrl) > 0)
	assert.Nil(suite.T(), rsp1.Message)

	order1, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order1)

	assert.True(suite.T(), order.Tax.Amount > order1.Tax.Amount)
	assert.True(suite.T(), order.TotalPaymentAmount > order1.TotalPaymentAmount)
	assert.NotNil(suite.T(), order1.BillingAddress)
	assert.Equal(suite.T(), "US", order1.BillingAddress.Country)
	assert.Equal(suite.T(), "Washington", order1.BillingAddress.City)
	assert.Equal(suite.T(), "98001", order1.BillingAddress.PostalCode)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_UserAddressDataRequired_CountryFieldNotFound_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	order, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Nil(suite.T(), order.BillingAddress)

	order.UserAddressDataRequired = true
	err = suite.service.updateOrder(context.TODO(), order)
	assert.NoError(suite.T(), err)

	expireYear := time.Now().AddDate(1, 0, 0)

	req1 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
	}

	rsp1 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req1, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Empty(suite.T(), rsp1.RedirectUrl)
	assert.Equal(suite.T(), orderErrorCreatePaymentRequiredFieldUserCountryNotFound, rsp1.Message)

	order1, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order1)

	assert.Equal(suite.T(), order.Tax.Amount, order1.Tax.Amount)
	assert.Equal(suite.T(), order.TotalPaymentAmount, order1.TotalPaymentAmount)
	assert.Nil(suite.T(), order1.BillingAddress)
}

/*func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_UserAddressDataRequired_ZipFieldNotFound_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	order, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Nil(suite.T(), order.BillingAddress)

	order.UserAddressDataRequired = true
	err = suite.service.updateOrder(context.TODO(), order)
	assert.NoError(suite.T(), err)

	expireYear := time.Now().AddDate(1, 0, 0)

	req1 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
			pkg.PaymentCreateFieldUserCountry:     "US",
			pkg.PaymentCreateFieldUserCity:        "Washington",
			pkg.PaymentCreateFieldUserZip:         "000000",
		},
	}

	rsp1 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req1, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Empty(suite.T(), rsp1.RedirectUrl)
	assert.Equal(suite.T(), orderErrorZipCodeNotFound, rsp1.Message)

	order1, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order1)

	assert.Equal(suite.T(), order.Tax.Amount, order1.Tax.Amount)
	assert.Equal(suite.T(), order.TotalPaymentAmount, order1.TotalPaymentAmount)
	assert.Nil(suite.T(), order1.BillingAddress)
}*/

func (suite *OrderTestSuite) TestOrder_CreateOrderByToken_Ok() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Email: &billing.TokenUserEmailValue{
				Value: "test@unit.test",
			},
			Phone: &billing.TokenUserPhoneValue{
				Value: "1234567890",
			},
			Name: &billing.TokenUserValue{
				Value: "Unit Test",
			},
			Ip: &billing.TokenUserIpValue{
				Value: "127.0.0.1",
			},
			Locale: &billing.TokenUserLocaleValue{
				Value: "ru",
			},
			Address: &billing.OrderBillingAddress{
				Country:    "RU",
				City:       "St.Petersburg",
				PostalCode: "190000",
				State:      "SPE",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.project.Id,
			Currency:    "RUB",
			Amount:      100,
			Description: "test payment",
			Type:        billing.OrderType_simple,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.Empty(suite.T(), rsp.Message)
	assert.NotEmpty(suite.T(), rsp.Token)

	req1 := &billing.OrderCreateRequest{
		Token: rsp.Token,
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err = suite.service.OrderCreateProcess(context.TODO(), req1, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp1 := rsp0.Item
	assert.NotEmpty(suite.T(), rsp1.Id)
	assert.Equal(suite.T(), req.Settings.ProjectId, rsp1.Project.Id)
	assert.Equal(suite.T(), req.Settings.Description, rsp1.Description)
}

func (suite *OrderTestSuite) TestOrder_updateOrder_NotifyKeys_Ok() {
	shoulBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		ProjectId:     suite.projectWithKeyProducts.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products:   suite.keyProductIds,
		Type:       billing.OrderType_key,
		PlatformId: "steam",
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)
	shoulBe.Nil(err)
	shoulBe.EqualValues(200, rsp.Status)

	order := rsp.Item
	order.Status = constant.OrderPublicStatusProcessed
	err = suite.service.updateOrder(context.TODO(), order)
	shoulBe.Nil(err)
}

func (suite *OrderTestSuite) TestOrder_updateOrder_NotifyKeysRejected_Ok() {
	shoulBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		ProjectId:     suite.projectWithKeyProducts.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products:   suite.keyProductIds,
		Type:       billing.OrderType_key,
		PlatformId: "steam",
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)
	shoulBe.Nil(err)
	shoulBe.EqualValues(200, rsp.Status)

	order := rsp.Item
	order.Status = constant.OrderPublicStatusRejected
	err = suite.service.updateOrder(context.TODO(), order)
	shoulBe.Nil(err)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_UuidNotFound_Error() {
	req := &grpc.PaymentFormJsonDataRequest{
		OrderId: primitive.NewObjectID().Hex(),
	}
	rsp := &grpc.PaymentFormJsonDataResponse{}
	err := suite.service.PaymentFormJsonDataProcess(context.TODO(), req, rsp)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), orderErrorNotFound, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_NewCookie_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Ip: "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	req1 := &grpc.PaymentFormJsonDataRequest{
		OrderId: rsp.Uuid,
		Scheme:  "http",
		Host:    "127.0.0.1",
	}
	rsp1 := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.NotEmpty(suite.T(), rsp1.Cookie)

	browserCustomer, err := suite.service.decryptBrowserCookie(rsp1.Cookie)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), browserCustomer)
	assert.NotEmpty(suite.T(), browserCustomer.CustomerId)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_ExistCookie_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	req1 := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Email: &billing.TokenUserEmailValue{
				Value: "test@unit.test",
			},
			Phone: &billing.TokenUserPhoneValue{
				Value: "1234567890",
			},
			Name: &billing.TokenUserValue{
				Value: "Unit Test",
			},
			Ip: &billing.TokenUserIpValue{
				Value: "127.0.0.1",
			},
			Locale: &billing.TokenUserLocaleValue{
				Value: "ru",
			},
			Address: &billing.OrderBillingAddress{
				Country:    "RU",
				City:       "St.Petersburg",
				PostalCode: "190000",
				State:      "SPE",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.project.Id,
			Currency:    "RUB",
			Amount:      100,
			Description: "test payment",
		},
	}
	customer, err := suite.service.createCustomer(context.TODO(), req1, suite.project)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), customer)

	browserCustomer := &BrowserCookieCustomer{
		CustomerId: customer.Id,
		Ip:         "127.0.0.1",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	cookie, err := suite.service.generateBrowserCookie(browserCustomer)
	assert.NoError(suite.T(), err)
	assert.NotEmpty(suite.T(), cookie)

	req2 := &grpc.PaymentFormJsonDataRequest{
		OrderId: rsp.Uuid,
		Scheme:  "http",
		Host:    "127.0.0.1",
		Cookie:  cookie,
		Ip:      "127.0.0.1",
	}
	rsp2 := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req2, rsp2)
	assert.NoError(suite.T(), err)
	assert.NotEmpty(suite.T(), rsp2.Cookie)

	browserCustomer, err = suite.service.decryptBrowserCookie(rsp2.Cookie)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), browserCustomer)
	assert.NotEmpty(suite.T(), browserCustomer.CustomerId)
	assert.Equal(suite.T(), int32(1), browserCustomer.SessionCount)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_NotOwnBankCard_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	order, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Nil(suite.T(), order.BillingAddress)

	req1 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldStoredCardId:    primitive.NewObjectID().Hex(),
		},
	}

	rsp1 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorRecurringCardNotOwnToUser, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_IsOrderCanBePaying_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	req1 := &grpc.IsOrderCanBePayingRequest{
		OrderId:   rsp.Uuid,
		ProjectId: rsp.GetProjectId(),
	}
	rsp1 := &grpc.IsOrderCanBePayingResponse{}
	err = suite.service.IsOrderCanBePaying(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotNil(suite.T(), rsp1.Item)
	assert.Equal(suite.T(), req1.ProjectId, rsp1.Item.GetProjectId())
	assert.Equal(suite.T(), req1.OrderId, rsp1.Item.Uuid)
}

func (suite *OrderTestSuite) TestOrder_IsOrderCanBePaying_IncorrectProject_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	req1 := &grpc.IsOrderCanBePayingRequest{
		OrderId:   rsp.Uuid,
		ProjectId: primitive.NewObjectID().Hex(),
	}
	rsp1 := &grpc.IsOrderCanBePayingResponse{}
	err = suite.service.IsOrderCanBePaying(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorOrderCreatedAnotherProject, rsp1.Message)
	assert.Nil(suite.T(), rsp1.Item)
}

func (suite *OrderTestSuite) TestOrder_IsOrderCanBePaying_HasEndedStatus_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	rsp.PrivateStatus = constant.OrderStatusProjectComplete
	err = suite.service.updateOrder(context.TODO(), rsp)
	assert.NoError(suite.T(), err)

	req1 := &grpc.IsOrderCanBePayingRequest{
		OrderId:   rsp.Uuid,
		ProjectId: rsp.GetProjectId(),
	}
	rsp1 := &grpc.IsOrderCanBePayingResponse{}
	err = suite.service.IsOrderCanBePaying(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Equal(suite.T(), orderErrorOrderAlreadyComplete, rsp1.Message)
	assert.Nil(suite.T(), rsp1.Item)
}

func (suite *OrderTestSuite) TestOrder_CreatePayment_ChangeCustomerData_Ok() {
	req := &billing.OrderCreateRequest{
		Type:      billing.OrderType_simple,
		ProjectId: suite.project.Id,
		Currency:  "RUB",
		Amount:    100,
		User: &billing.OrderUser{
			Email:  "test@unit.unit",
			Ip:     "127.0.0.1",
			Locale: "ru",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	customer1, err := suite.service.getCustomerById(context.TODO(), rsp.User.Id)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), customer1)
	assert.Equal(suite.T(), rsp.User.Id, customer1.Id)
	assert.Equal(suite.T(), rsp.User.Email, customer1.Email)
	assert.Equal(suite.T(), rsp.User.Ip, net.IP(customer1.Ip).String())
	assert.Len(suite.T(), customer1.Identity, 1)
	assert.Equal(suite.T(), rsp.User.Email, customer1.Identity[0].Value)
	assert.Empty(suite.T(), customer1.IpHistory)
	assert.Empty(suite.T(), customer1.AcceptLanguage)
	assert.Empty(suite.T(), customer1.AcceptLanguageHistory)
	assert.Equal(suite.T(), rsp.User.Locale, customer1.Locale)
	assert.Empty(suite.T(), customer1.LocaleHistory)
	assert.Empty(suite.T(), customer1.UserAgent)

	req1 := &grpc.PaymentFormJsonDataRequest{
		OrderId:   rsp.Uuid,
		Scheme:    "http",
		Host:      "localhost",
		Locale:    "en-US",
		Ip:        "127.0.0.2",
		UserAgent: "linux",
	}
	rsp1 := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)

	customer2, err := suite.service.getCustomerById(context.TODO(), rsp.User.Id)
	assert.NoError(suite.T(), err)

	order, err := suite.service.getOrderById(context.TODO(), rsp.Id)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.NotNil(suite.T(), order.User)
	assert.Equal(suite.T(), customer2.Id, order.User.Id)
	assert.Equal(suite.T(), order.User.Ip, "127.0.0.1")
	assert.Equal(suite.T(), order.User.Locale, customer2.LocaleHistory[0].Value)
	assert.Equal(suite.T(), order.User.Email, customer2.Email)
	assert.False(suite.T(), order.UserAddressDataRequired)

	assert.NotNil(suite.T(), customer2)
	assert.Equal(suite.T(), customer1.Id, customer2.Id)
	assert.Equal(suite.T(), customer1.Email, customer2.Email)
	assert.Equal(suite.T(), req1.Ip, net.IP(customer2.Ip).String())
	assert.NotEmpty(suite.T(), customer2.IpHistory)
	assert.Len(suite.T(), customer2.IpHistory, 1)
	assert.Equal(suite.T(), customer2.IpHistory[0].Ip, customer1.Ip)
	assert.Len(suite.T(), customer2.Identity, 1)
	assert.Equal(suite.T(), rsp.User.Email, customer2.Identity[0].Value)
	assert.Equal(suite.T(), req1.Locale, customer2.AcceptLanguage)
	assert.Empty(suite.T(), customer2.AcceptLanguageHistory)
	assert.Equal(suite.T(), "en-US", customer2.Locale)
	assert.NotEmpty(suite.T(), customer2.LocaleHistory)
	assert.Len(suite.T(), customer2.LocaleHistory, 1)
	assert.Equal(suite.T(), customer1.Locale, customer2.LocaleHistory[0].Value)
	assert.Equal(suite.T(), req1.UserAgent, customer2.UserAgent)

	expireYear := time.Now().AddDate(1, 0, 0)

	req2 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test123@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "MR. CARD HOLDER",
			pkg.PaymentCreateFieldUserCountry:     "US",
			pkg.PaymentCreateFieldUserZip:         "98001",
		},
		Ip:             "127.0.0.3",
		AcceptLanguage: "fr-CA",
		UserAgent:      "windows",
	}
	rsp2 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req2, rsp2)
	assert.NoError(suite.T(), err)

	order, err = suite.service.getOrderById(context.TODO(), rsp.Id)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Equal(suite.T(), int32(constant.OrderStatusPaymentSystemCreate), order.PrivateStatus)

	customer3, err := suite.service.getCustomerById(context.TODO(), rsp.User.Id)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), customer3)

	assert.Equal(suite.T(), customer2.Id, customer3.Id)
	assert.Equal(suite.T(), customer3.Id, order.User.Id)
	assert.Equal(suite.T(), order.User.Ip, req2.Ip)
	assert.Equal(suite.T(), req.User.Locale, order.User.Locale)
	assert.Equal(suite.T(), "test123@unit.unit", order.User.Email)

	assert.Equal(suite.T(), order.User.Email, customer3.Email)
	assert.Equal(suite.T(), order.User.Ip, net.IP(customer3.Ip).String())
	assert.Len(suite.T(), customer3.IpHistory, 2)
	assert.Equal(suite.T(), customer2.Ip, customer3.IpHistory[1].Ip)
	assert.Equal(suite.T(), customer1.Ip, customer3.IpHistory[0].Ip)

	assert.Len(suite.T(), customer3.Identity, 2)
	assert.Equal(suite.T(), order.User.Email, customer3.Identity[1].Value)
	assert.Equal(suite.T(), customer2.Email, customer3.Identity[0].Value)

	assert.Equal(suite.T(), req2.AcceptLanguage, customer3.AcceptLanguage)
	assert.Len(suite.T(), customer3.AcceptLanguageHistory, 1)
	assert.Equal(suite.T(), customer2.AcceptLanguage, customer3.AcceptLanguageHistory[0].Value)

	assert.Equal(suite.T(), req2.AcceptLanguage, customer3.Locale)
	assert.Len(suite.T(), customer3.LocaleHistory, 2)
	assert.Equal(suite.T(), customer1.Locale, customer3.LocaleHistory[0].Value)
	assert.Equal(suite.T(), customer2.Locale, customer3.LocaleHistory[1].Value)
	assert.Equal(suite.T(), req2.UserAgent, customer3.UserAgent)
}

func (suite *OrderTestSuite) TestOrder_GetPublicStatus() {
	order := &billing.Order{}

	order.PrivateStatus = constant.OrderStatusNew
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusCreated)

	order.PrivateStatus = constant.OrderStatusPaymentSystemCreate
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusCreated)

	order.PrivateStatus = constant.OrderStatusPaymentSystemCanceled
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusCanceled)

	order.PrivateStatus = constant.OrderStatusPaymentSystemRejectOnCreate
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusRejected)

	order.PrivateStatus = constant.OrderStatusPaymentSystemReject
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusRejected)

	order.PrivateStatus = constant.OrderStatusProjectReject
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusRejected)

	order.PrivateStatus = constant.OrderStatusPaymentSystemDeclined
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusRejected)

	order.PrivateStatus = constant.OrderStatusPaymentSystemComplete
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusProcessed)

	order.PrivateStatus = constant.OrderStatusProjectComplete
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusProcessed)

	order.PrivateStatus = constant.OrderStatusRefund
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusRefunded)

	order.PrivateStatus = constant.OrderStatusChargeback
	assert.Equal(suite.T(), order.GetPublicStatus(), constant.OrderPublicStatusChargeback)
}

func (suite *OrderTestSuite) TestOrder_GetReceiptUserEmail() {
	order := &billing.Order{}
	assert.Empty(suite.T(), order.GetReceiptUserEmail())

	order.User = &billing.OrderUser{}
	assert.Empty(suite.T(), order.GetReceiptUserEmail())

	order.User.Email = "test@test.com"
	assert.NotEmpty(suite.T(), order.GetReceiptUserEmail())
	assert.Equal(suite.T(), order.GetReceiptUserEmail(), "test@test.com")
}

func (suite *OrderTestSuite) TestOrder_GetReceiptUserPhone() {
	order := &billing.Order{}
	assert.Empty(suite.T(), order.GetReceiptUserPhone())

	order.User = &billing.OrderUser{}
	assert.Empty(suite.T(), order.GetReceiptUserPhone())

	order.User.Phone = "79111234567"
	assert.NotEmpty(suite.T(), order.GetReceiptUserPhone())
	assert.Equal(suite.T(), order.GetReceiptUserPhone(), "79111234567")
}

func (suite *OrderTestSuite) TestOrder_GetCountry() {
	order := &billing.Order{}
	assert.Empty(suite.T(), order.GetCountry())

	order.User = &billing.OrderUser{
		Address: &billing.OrderBillingAddress{
			Country: "RU",
		},
	}
	assert.NotEmpty(suite.T(), order.GetCountry())
	assert.Equal(suite.T(), order.GetCountry(), "RU")

	order.BillingAddress = &billing.OrderBillingAddress{
		Country: "CY",
	}
	assert.NotEmpty(suite.T(), order.GetCountry())
	assert.Equal(suite.T(), order.GetCountry(), "CY")
}

func (suite *OrderTestSuite) TestOrder_GetState() {
	order := &billing.Order{}
	assert.Empty(suite.T(), order.GetState())

	order.User = &billing.OrderUser{
		Address: &billing.OrderBillingAddress{
			Country: "US",
			State:   "AL",
		},
	}
	assert.NotEmpty(suite.T(), order.GetState())
	assert.Equal(suite.T(), order.GetState(), "AL")

	order.BillingAddress = &billing.OrderBillingAddress{
		Country: "US",
		State:   "MN",
	}
	assert.NotEmpty(suite.T(), order.GetState())
	assert.Equal(suite.T(), order.GetState(), "MN")
}

func (suite *OrderTestSuite) TestOrder_SetNotificationStatus() {
	order := &billing.Order{}
	assert.Nil(suite.T(), order.IsNotificationsSent)

	order.SetNotificationStatus("somekey", true)
	assert.NotNil(suite.T(), order.IsNotificationsSent)
	assert.Equal(suite.T(), len(order.IsNotificationsSent), 1)
	assert.Equal(suite.T(), order.IsNotificationsSent["somekey"], true)
}

func (suite *OrderTestSuite) TestOrder_GetNotificationStatus() {
	order := &billing.Order{}
	assert.Nil(suite.T(), order.IsNotificationsSent)

	ns := order.GetNotificationStatus("somekey")
	assert.False(suite.T(), ns)

	order.IsNotificationsSent = make(map[string]bool)
	order.IsNotificationsSent["somekey"] = true

	ns = order.GetNotificationStatus("somekey")
	assert.True(suite.T(), ns)
}

func (suite *OrderTestSuite) TestOrder_orderNotifyMerchant_Ok() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	order := rsp0.Item

	ps := order.GetPublicStatus()
	assert.Equal(suite.T(), ps, constant.OrderPublicStatusCreated)
	nS := order.GetNotificationStatus(ps)
	assert.False(suite.T(), nS)
	assert.False(suite.T(), order.GetNotificationStatus(constant.OrderPublicStatusProcessed))
	assert.Equal(suite.T(), len(order.IsNotificationsSent), 0)

	order.PrivateStatus = constant.OrderStatusProjectComplete
	err = suite.service.updateOrder(context.TODO(), order)
	assert.NoError(suite.T(), err)

	ps = order.GetPublicStatus()
	assert.Equal(suite.T(), ps, constant.OrderPublicStatusProcessed)
	nS = order.GetNotificationStatus(ps)
	assert.True(suite.T(), nS)
	assert.Equal(suite.T(), len(order.IsNotificationsSent), 1)
}

func (suite *OrderTestSuite) TestCardpay_fillPaymentDataCrypto() {
	var (
		name    = "Bitcoin"
		address = "1ByR2GSfDMuFGVoUzh4a5pzgrVuoTdr8wU"
	)

	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	order := rsp0.Item

	order.PaymentMethod = &billing.PaymentMethodOrder{
		Name: name,
	}
	order.PaymentMethodTxnParams = make(map[string]string)
	order.PaymentMethodTxnParams[pkg.PaymentCreateFieldCrypto] = address
	order.PaymentMethodTxnParams[pkg.TxnParamsFieldCryptoTransactionId] = "7d8c131c-092c-4a5b-83ed-5137ecb9b083"
	order.PaymentMethodTxnParams[pkg.TxnParamsFieldCryptoAmount] = "0.0001"
	order.PaymentMethodTxnParams[pkg.TxnParamsFieldCryptoCurrency] = "BTC"

	err = suite.service.fillPaymentDataCrypto(order)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), order.PaymentMethodPayerAccount, address)
	assert.NotNil(suite.T(), order.PaymentMethod.CryptoCurrency)
	assert.Equal(suite.T(), order.PaymentMethod.CryptoCurrency.Brand, name)
	assert.Equal(suite.T(), order.PaymentMethod.CryptoCurrency.Address, address)
}

func (suite *OrderTestSuite) TestCardpay_fillPaymentDataEwallet() {
	var (
		name    = "yamoney"
		account = "41001811131268"
	)
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	order := rsp0.Item

	order.PaymentMethod = &billing.PaymentMethodOrder{
		Name: name,
	}
	order.PaymentMethodTxnParams = make(map[string]string)
	order.PaymentMethodTxnParams[pkg.PaymentCreateFieldEWallet] = account

	err = suite.service.fillPaymentDataEwallet(order)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), order.PaymentMethodPayerAccount, account)
	assert.NotNil(suite.T(), order.PaymentMethod.Wallet)
	assert.Equal(suite.T(), order.PaymentMethod.Wallet.Brand, name)
	assert.Equal(suite.T(), order.PaymentMethod.Wallet.Account, account)
}

func (suite *OrderTestSuite) TestCardpay_fillPaymentDataCard() {
	var (
		name      = "bank_card"
		maskedPan = "444444******4448"
		expMonth  = "10"
		expYear   = "2021"
		cardBrand = "VISA"
	)

	req := &billing.OrderCreateRequest{
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	order := rsp0.Item

	order.PaymentMethod = &billing.PaymentMethodOrder{
		Name: name,
	}
	order.PaymentMethodTxnParams = make(map[string]string)
	order.PaymentMethodTxnParams[pkg.TxnParamsFieldBankCardIs3DS] = "1"

	order.PaymentRequisites = make(map[string]string)
	order.PaymentRequisites["card_brand"] = cardBrand
	order.PaymentRequisites["pan"] = maskedPan
	order.PaymentRequisites["month"] = expMonth
	order.PaymentRequisites["year"] = expYear

	err = suite.service.fillPaymentDataCard(order)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), order.PaymentMethodPayerAccount, maskedPan)
	assert.NotNil(suite.T(), order.PaymentMethod.Card)
	assert.Equal(suite.T(), order.PaymentMethod.Card.Brand, cardBrand)
	assert.Equal(suite.T(), order.PaymentMethod.Card.Masked, maskedPan)
	assert.Equal(suite.T(), order.PaymentMethod.Card.First6, "444444")
	assert.Equal(suite.T(), order.PaymentMethod.Card.Last4, "4448")
	assert.Equal(suite.T(), order.PaymentMethod.Card.ExpiryMonth, expMonth)
	assert.Equal(suite.T(), order.PaymentMethod.Card.ExpiryYear, expYear)
	assert.Equal(suite.T(), order.PaymentMethod.Card.Secure3D, true)
	assert.NotEmpty(suite.T(), order.PaymentMethod.Card.Fingerprint)
}

func (suite *OrderTestSuite) TestBillingService_SetUserNotifySales_Ok() {

	notifyEmail := "test@test.ru"

	req := &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.False(suite.T(), rsp.NotifySale)
	assert.Empty(suite.T(), rsp.NotifySaleEmail)

	var data []*grpc.NotifyUserSales
	cursor, err := suite.service.db.Collection(collectionNotifySales).Find(context.TODO(), bson.M{"email": notifyEmail})
	assert.Nil(suite.T(), err)
	err = cursor.All(context.TODO(), &data)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), len(data), 0)

	req2 := &grpc.SetUserNotifyRequest{
		OrderUuid:          rsp.Uuid,
		Email:              notifyEmail,
		EnableNotification: true,
	}
	eRes := &grpc.EmptyResponse{}
	err = suite.service.SetUserNotifySales(context.TODO(), req2, eRes)
	assert.Nil(suite.T(), err)

	order, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.Nil(suite.T(), err)
	assert.True(suite.T(), order.NotifySale)
	assert.Equal(suite.T(), order.NotifySaleEmail, notifyEmail)

	cursor, err = suite.service.db.Collection(collectionNotifySales).Find(context.TODO(), bson.M{"email": notifyEmail})
	assert.Nil(suite.T(), err)
	err = cursor.All(context.TODO(), &data)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), len(data), 1)

	customer, err := suite.service.getCustomerById(context.TODO(), rsp.User.Id)
	assert.NoError(suite.T(), err)
	assert.True(suite.T(), customer.NotifySale)
	assert.Equal(suite.T(), customer.NotifySaleEmail, notifyEmail)
}

func (suite *OrderTestSuite) TestBillingService_SetUserNotifyNewRegion_Ok() {

	notifyEmail := "test@test.ru"

	req := &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item
	assert.False(suite.T(), rsp.User.NotifyNewRegion)
	assert.Empty(suite.T(), rsp.User.NotifyNewRegionEmail)

	var data []*grpc.NotifyUserNewRegion
	cursor, err := suite.service.db.Collection(collectionNotifyNewRegion).Find(context.TODO(), bson.M{"email": notifyEmail})
	assert.Nil(suite.T(), err)
	err = cursor.All(context.TODO(), &data)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), len(data), 0)

	rsp.CountryRestriction = &billing.CountryRestriction{
		IsoCodeA2:     "RU",
		ChangeAllowed: false,
	}
	err = suite.service.updateOrder(context.TODO(), rsp)
	assert.Nil(suite.T(), err)

	req2 := &grpc.SetUserNotifyRequest{
		OrderUuid:          rsp.Uuid,
		Email:              notifyEmail,
		EnableNotification: true,
	}
	eRes := &grpc.EmptyResponse{}
	err = suite.service.SetUserNotifyNewRegion(context.TODO(), req2, eRes)
	assert.Nil(suite.T(), err)

	order, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.Nil(suite.T(), err)
	assert.True(suite.T(), order.User.NotifyNewRegion)
	assert.Equal(suite.T(), order.User.NotifyNewRegionEmail, notifyEmail)

	cursor, err = suite.service.db.Collection(collectionNotifyNewRegion).Find(context.TODO(), bson.M{"email": notifyEmail})
	assert.Nil(suite.T(), err)
	err = cursor.All(context.TODO(), &data)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), len(data), 1)

	customer, err := suite.service.getCustomerById(context.TODO(), rsp.User.Id)
	assert.NoError(suite.T(), err)
	assert.True(suite.T(), customer.NotifyNewRegion)
	assert.Equal(suite.T(), customer.NotifyNewRegionEmail, notifyEmail)
}

func (suite *OrderTestSuite) TestBillingService_OrderCreateProcess_CountryRestrictions() {
	req := &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		User: &billing.OrderUser{
			Email:   "test@unit.unit",
			Ip:      "127.0.0.1",
			Address: &billing.OrderBillingAddress{},
		},
		Type: billing.OrderType_simple,
	}

	// payments allowed
	req.User.Address.Country = "RU"
	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	order := rsp0.Item
	assert.NotNil(suite.T(), order.CountryRestriction)
	assert.Equal(suite.T(), order.CountryRestriction.IsoCodeA2, "RU")
	assert.True(suite.T(), order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), order.CountryRestriction.ChangeAllowed)
	assert.False(suite.T(), order.UserAddressDataRequired)
	assert.Equal(suite.T(), order.PrivateStatus, int32(constant.OrderStatusNew))

	// payments not allowed but country change allowed
	req.User.Address.Country = "UA"
	err = suite.service.OrderCreateProcess(context.TODO(), req, rsp0)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	order = rsp0.Item
	assert.NotNil(suite.T(), order.CountryRestriction)
	assert.Equal(suite.T(), order.CountryRestriction.IsoCodeA2, "UA")
	assert.False(suite.T(), order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), order.UserAddressDataRequired)
	assert.Equal(suite.T(), order.PrivateStatus, int32(constant.OrderStatusNew))

	// payments not allowed and country change not allowed too
	req.User.Address.Country = "BY"
	err = suite.service.OrderCreateProcess(context.TODO(), req, rsp0)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderCountryPaymentRestrictedError, rsp0.Message)
}

func (suite *OrderTestSuite) TestBillingService_processPaymentFormData_CountryRestrictions() {
	req := &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		User: &billing.OrderUser{
			Email:   "test@unit.unit",
			Ip:      "127.0.0.1",
			Address: &billing.OrderBillingAddress{},
		},
		Type: billing.OrderType_simple,
	}
	order := &billing.Order{}

	// payments allowed
	req.User.Address.Country = "RU"
	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	order = rsp0.Item
	assert.NotNil(suite.T(), order.CountryRestriction)
	assert.Equal(suite.T(), order.CountryRestriction.IsoCodeA2, "RU")
	assert.True(suite.T(), order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), order.CountryRestriction.ChangeAllowed)
	assert.False(suite.T(), order.UserAddressDataRequired)
	assert.Equal(suite.T(), order.PrivateStatus, int32(constant.OrderStatusNew))

	order.UserAddressDataRequired = true
	err = suite.service.updateOrder(context.TODO(), order)
	assert.NoError(suite.T(), err)

	// payments disallowed
	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         order.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldPan:             "4000000000000002",
		pkg.PaymentCreateFieldCvv:             "123",
		pkg.PaymentCreateFieldMonth:           "02",
		pkg.PaymentCreateFieldYear:            "2100",
		pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		pkg.PaymentCreateFieldUserCountry:     "UA",
		pkg.PaymentCreateFieldUserCity:        "Kiev",
		pkg.PaymentCreateFieldUserZip:         "02154",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.processPaymentFormData(context.TODO())
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), processor.checked.order)
	assert.NotNil(suite.T(), processor.checked.project)
	assert.NotNil(suite.T(), processor.checked.paymentMethod)
	assert.Equal(suite.T(), processor.checked.order.CountryRestriction.IsoCodeA2, "UA")
	assert.False(suite.T(), processor.checked.order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), processor.checked.order.CountryRestriction.ChangeAllowed)
}

func (suite *OrderTestSuite) TestBillingService_PaymentCreateProcess_CountryRestrictions() {
	req := &billing.OrderCreateRequest{
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		User: &billing.OrderUser{
			Email:   "test@unit.unit",
			Ip:      "127.0.0.1",
			Address: &billing.OrderBillingAddress{},
		},
		Type: billing.OrderType_simple,
	}
	order := &billing.Order{}

	// payments allowed
	req.User.Address.Country = "RU"
	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	order = rsp0.Item

	order.UserAddressDataRequired = true
	err = suite.service.updateOrder(context.TODO(), order)
	assert.NoError(suite.T(), err)

	// payments disallowed
	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         order.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldPan:             "4000000000000002",
		pkg.PaymentCreateFieldCvv:             "123",
		pkg.PaymentCreateFieldMonth:           "02",
		pkg.PaymentCreateFieldYear:            "2100",
		pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		pkg.PaymentCreateFieldUserCountry:     "UA",
		pkg.PaymentCreateFieldUserCity:        "Kiev",
		pkg.PaymentCreateFieldUserZip:         "02154",
	}

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: data,
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusForbidden, rsp.Status)
	assert.Equal(suite.T(), orderCountryPaymentRestrictedError, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_UserAddressDataRequired_USAZipRequired_Error() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp := rsp0.Item

	order, err := suite.service.getOrderByUuid(context.TODO(), rsp.Uuid)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Nil(suite.T(), order.BillingAddress)

	order.UserAddressDataRequired = true
	err = suite.service.updateOrder(context.TODO(), order)
	assert.NoError(suite.T(), err)

	expireYear := time.Now().AddDate(1, 0, 0)

	req1 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
			pkg.PaymentCreateFieldUserCountry:     "US",
			pkg.PaymentCreateFieldUserZip:         "",
		},
	}

	rsp1 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req1, rsp1)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp1.Status)
	assert.Empty(suite.T(), rsp1.RedirectUrl)
	assert.Equal(suite.T(), orderErrorCreatePaymentRequiredFieldUserZipNotFound, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentCallbackProcess_AccountingEntries_Ok() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithProducts.Id,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		Products:    suite.productIds,
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_product,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)

	req1 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Item.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            time.Now().AddDate(1, 0, 0).Format("2006"),
			pkg.PaymentCreateFieldHolder:          "MR. CARD HOLDER",
		},
		Ip: "127.0.0.1",
	}

	rsp1 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req1, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)

	oid, err := primitive.ObjectIDFromHex(rsp.Item.Id)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var order *billing.Order
	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order)
	assert.NotNil(suite.T(), order)
	assert.IsType(suite.T(), &billing.Order{}, order)

	callbackRequest := &billing.CardPayPaymentCallback{
		PaymentMethod: suite.paymentMethod.ExternalId,
		CallbackTime:  time.Now().Format("2006-01-02T15:04:05Z"),
		MerchantOrder: &billing.CardPayMerchantOrder{
			Id:          rsp.Item.Id,
			Description: rsp.Item.Description,
			Items: []*billing.CardPayItem{
				{
					Name:        order.Items[0].Name,
					Description: order.Items[0].Name,
					Count:       1,
					Price:       order.Items[0].Amount,
				},
			},
		},
		CardAccount: &billing.CallbackCardPayBankCardAccount{
			Holder:             order.PaymentRequisites[pkg.PaymentCreateFieldHolder],
			IssuingCountryCode: "RU",
			MaskedPan:          order.PaymentRequisites[pkg.PaymentCreateFieldPan],
			Token:              primitive.NewObjectID().Hex(),
		},
		Customer: &billing.CardPayCustomer{
			Email:  rsp.Item.User.Email,
			Ip:     rsp.Item.User.Ip,
			Id:     rsp.Item.ProjectAccount,
			Locale: "Europe/Moscow",
		},
		PaymentData: &billing.CallbackCardPayPaymentData{
			Id:          primitive.NewObjectID().Hex(),
			Amount:      order.TotalPaymentAmount,
			Currency:    order.Currency,
			Description: order.Description,
			Is_3D:       true,
			Rrn:         primitive.NewObjectID().Hex(),
			Status:      pkg.CardPayPaymentResponseStatusCompleted,
		},
	}

	buf, err := json.Marshal(callbackRequest)
	assert.NoError(suite.T(), err)

	hash := sha512.New()
	hash.Write([]byte(string(buf) + order.PaymentMethod.Params.SecretCallback))

	callbackData := &grpc.PaymentNotifyRequest{
		OrderId:   order.Id,
		Request:   buf,
		Signature: hex.EncodeToString(hash.Sum(nil)),
	}

	callbackResponse := &grpc.PaymentNotifyResponse{}
	err = suite.service.PaymentCallbackProcess(context.TODO(), callbackData, callbackResponse)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.StatusOK, callbackResponse.Status)

	oid, err = primitive.ObjectIDFromHex(order.Id)
	assert.NoError(suite.T(), err)
	filter = bson.M{"_id": oid}

	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order)
	assert.NotNil(suite.T(), order)
	assert.IsType(suite.T(), &billing.Order{}, order)
	assert.Equal(suite.T(), int32(constant.OrderStatusPaymentSystemComplete), order.PrivateStatus)

	filter = bson.M{"source.id": oid, "source.type": collectionOrder}

	var accountingEntries []*billing.AccountingEntry
	cursor, err := suite.service.db.Collection(collectionAccountingEntry).Find(context.TODO(), filter)
	assert.NoError(suite.T(), err)
	err = cursor.All(context.TODO(), &accountingEntries)
	assert.NoError(suite.T(), err)
	assert.NotEmpty(suite.T(), accountingEntries)
}

func (suite *OrderTestSuite) TestOrder_PaymentCallbackProcess_Error() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectFixedAmount.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	expireYear := time.Now().AddDate(1, 0, 0)

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         order.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
		Ip: "127.0.0.1",
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)

	oid, err := primitive.ObjectIDFromHex(order.Id)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var order1 *billing.Order
	err = suite.service.db.Collection(collectionOrder).FindOne(context.TODO(), filter).Decode(&order1)
	suite.NotNil(suite.T(), order1)

	callbackRequest := &billing.CardPayPaymentCallback{
		PaymentMethod: suite.paymentMethod.ExternalId,
		CallbackTime:  time.Now().Format("2006-01-02T15:04:05Z"),
		MerchantOrder: &billing.CardPayMerchantOrder{
			Id:          order.Id,
			Description: order.Description,
		},
		CardAccount: &billing.CallbackCardPayBankCardAccount{
			Holder:             order.PaymentRequisites[pkg.PaymentCreateFieldHolder],
			IssuingCountryCode: "RU",
			MaskedPan:          order.PaymentRequisites[pkg.PaymentCreateFieldPan],
			Token:              primitive.NewObjectID().Hex(),
		},
		Customer: &billing.CardPayCustomer{
			Email:  order.User.Email,
			Ip:     order.User.Ip,
			Id:     order.ProjectAccount,
			Locale: "Europe/Moscow",
		},
		PaymentData: &billing.CallbackCardPayPaymentData{
			Id:          primitive.NewObjectID().Hex(),
			Amount:      123,
			Currency:    order1.Currency,
			Description: order.Description,
			Is_3D:       true,
			Rrn:         primitive.NewObjectID().Hex(),
			Status:      pkg.CardPayPaymentResponseStatusCompleted,
		},
	}

	buf, err := json.Marshal(callbackRequest)
	assert.Nil(suite.T(), err)

	hash := sha512.New()
	hash.Write([]byte(string(buf) + order1.PaymentMethod.Params.SecretCallback))

	callbackData := &grpc.PaymentNotifyRequest{
		OrderId:   order.Id,
		Request:   buf,
		Signature: hex.EncodeToString(hash.Sum(nil)),
	}

	callbackResponse := &grpc.PaymentNotifyResponse{}
	err = suite.service.PaymentCallbackProcess(context.TODO(), callbackData, callbackResponse)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.StatusErrorValidation, callbackResponse.Status)

	filter = bson.M{"source.id": oid, "source.type": collectionOrder}

	var accountingEntries []*billing.AccountingEntry
	cursor, err := suite.service.db.Collection(collectionAccountingEntry).Find(context.TODO(), filter)
	assert.NoError(suite.T(), err)
	err = cursor.All(context.TODO(), &accountingEntries)
	assert.NoError(suite.T(), err)
	assert.Empty(suite.T(), accountingEntries)

	order, err = suite.service.getOrderById(context.TODO(), order.Id)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
}

func (suite *OrderTestSuite) Test_processPaylinkKeyProducts_error() {
	shouldBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithKeyProducts.Id,
		Currency:    "USD",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type:       billing.OrderType_key,
		PlatformId: "steam",
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)
	shouldBe.Nil(err)
	shouldBe.EqualValues(400, rsp1.Status)
	shouldBe.NotEmpty(rsp1.Message)

	req = &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithProducts.Id,
		Currency:    "USD",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type:       billing.OrderType_product,
		PlatformId: "steam",
	}

	rsp1 = &grpc.OrderCreateProcessResponse{}
	err = suite.service.OrderCreateProcess(context.TODO(), req, rsp1)
	shouldBe.Nil(err)
	shouldBe.EqualValues(400, rsp1.Status)
	shouldBe.NotEmpty(rsp1.Message)
}

func (suite *OrderTestSuite) Test_ProcessOrderKeyProducts() {
	shouldBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithKeyProducts.Id,
		Currency:    "USD",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type:       billing.OrderType_key,
		PlatformId: "steam",
	}

	req.Products = append(req.Products, suite.keyProductIds[0])

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	shouldBe.Nil(err)
	shouldBe.EqualValuesf(pkg.ResponseStatusOk, rsp1.Status, "%s", rsp1.Message)
	order := rsp1.Item

	_, err = suite.service.ProcessOrderKeyProducts(context.TODO(), order)
	shouldBe.Nil(err)
	shouldBe.NotEmpty(order.Items)
	shouldBe.Equal(suite.keyProductIds[0], order.Items[0].Id)
}

func (suite *OrderTestSuite) Test_ChangeCodeInOrder() {
	shouldBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithKeyProducts.Id,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products:   suite.keyProductIds,
		PlatformId: "steam",
		Type:       billing.OrderType_key,
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)
	shouldBe.Nil(err)
	shouldBe.Equal(pkg.ResponseStatusOk, rsp1.Status)

	order := rsp1.Item
	order.PrivateStatus = constant.OrderStatusPaymentSystemComplete

	shouldBe.Nil(suite.service.updateOrder(context.TODO(), order))

	keyProductId := suite.keyProductIds[0]

	codeRsp := &grpc.ChangeCodeInOrderResponse{}
	err = suite.service.ChangeCodeInOrder(context.TODO(), &grpc.ChangeCodeInOrderRequest{OrderId: rsp1.Item.Uuid, KeyProductId: keyProductId}, codeRsp)
	shouldBe.Nil(err)
	shouldBe.Equal(pkg.ResponseStatusOk, codeRsp.Status)
	shouldBe.EqualValues(constant.OrderStatusItemReplaced, codeRsp.Order.PrivateStatus)
}

func (suite *OrderTestSuite) Test_ChangePlatformInForm() {
	shouldBe := require.New(suite.T())
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.projectWithKeyProducts.Id,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products:   suite.keyProductIds,
		PlatformId: "steam",
		Type:       billing.OrderType_key,
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)
	shouldBe.Nil(err)
	shouldBe.Equal(pkg.ResponseStatusOk, rsp1.Status)

	order := rsp1.Item
	codeRsp := &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPlatformChanged(context.TODO(), &grpc.PaymentFormUserChangePlatformRequest{OrderId: order.Uuid, Platform: "gog"}, codeRsp)
	shouldBe.Nil(err)
	shouldBe.EqualValuesf(pkg.ResponseStatusOk, codeRsp.Status, "%v", codeRsp.Message)

	codeRsp = &grpc.PaymentFormDataChangeResponse{}
	err = suite.service.PaymentFormPlatformChanged(context.TODO(), &grpc.PaymentFormUserChangePlatformRequest{OrderId: order.Uuid, Platform: "xbox"}, codeRsp)
	shouldBe.Nil(err)
	shouldBe.Equal(pkg.ResponseStatusBadData, codeRsp.Status)
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_KeyProductReservation_Error() {
	shouldBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_key,
		ProjectId:   suite.projectWithKeyProducts.Id,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products:   suite.keyProductIds,
		PlatformId: "steam",
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	shouldBe.Nil(err)
	shouldBe.Equal(pkg.ResponseStatusOk, rsp1.Status)
	order := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         order.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldPan:             "4000000000000002",
		pkg.PaymentCreateFieldCvv:             "123",
		pkg.PaymentCreateFieldMonth:           "02",
		pkg.PaymentCreateFieldYear:            "2100",
		pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
	}

	// simulate not available product
	order.Products = append(order.Products, primitive.NewObjectID().Hex())

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.reserveKeysForOrder(context.TODO(), order)

	shouldBe.Error(err)
	shouldBe.EqualValues(0, len(order.Keys))
}

func (suite *OrderTestSuite) TestOrder_ProcessPaymentFormData_KeyProductReservation_Ok() {
	shouldBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_key,
		ProjectId:   suite.projectWithKeyProducts.Id,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Products:   suite.keyProductIds,
		PlatformId: "steam",
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	shouldBe.Nil(err)
	shouldBe.Equal(rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	data := map[string]string{
		pkg.PaymentCreateFieldOrderId:         order.Uuid,
		pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
		pkg.PaymentCreateFieldEmail:           "test@unit.unit",
		pkg.PaymentCreateFieldPan:             "4000000000000002",
		pkg.PaymentCreateFieldCvv:             "123",
		pkg.PaymentCreateFieldMonth:           "02",
		pkg.PaymentCreateFieldYear:            "2100",
		pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
	}

	processor := &PaymentCreateProcessor{service: suite.service, data: data}
	err = processor.reserveKeysForOrder(context.TODO(), order)

	shouldBe.Nil(err)
	shouldBe.EqualValues(len(suite.keyProductIds), len(order.Keys))
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_AllPaymentMethods() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "RUB",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
			Address: &billing.OrderBillingAddress{
				Country: "RU",
			},
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item
	assert.NotNil(suite.T(), order.CountryRestriction)
	assert.Equal(suite.T(), order.CountryRestriction.IsoCodeA2, "RU")
	assert.True(suite.T(), order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), order.CountryRestriction.ChangeAllowed)
	assert.False(suite.T(), order.UserAddressDataRequired)
	assert.Equal(suite.T(), order.PrivateStatus, int32(constant.OrderStatusNew))

	req1 := &grpc.PaymentFormJsonDataRequest{
		OrderId: order.Uuid,
		Scheme:  "https",
		Host:    "unit.test",
		Ip:      "127.0.0.1",
	}
	rsp := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp)

	assert.Nil(suite.T(), err)
	assert.True(suite.T(), len(rsp.Item.PaymentMethods) > 0)
	assert.True(suite.T(), len(rsp.Item.PaymentMethods[0].Id) > 0)
	assert.Len(suite.T(), rsp.Item.PaymentMethods, 6)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_OnePaymentMethods() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_simple,
		ProjectId:     suite.project.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Currency:      "UAH",
		Amount:        100,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
			Address: &billing.OrderBillingAddress{
				Country: "RU",
			},
		},
	}

	suite.service.supportedCurrencies = append(suite.service.supportedCurrencies, "UAH")
	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item
	assert.NotNil(suite.T(), order.CountryRestriction)
	assert.Equal(suite.T(), order.CountryRestriction.IsoCodeA2, "RU")
	assert.True(suite.T(), order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), order.CountryRestriction.ChangeAllowed)
	assert.False(suite.T(), order.UserAddressDataRequired)
	assert.Equal(suite.T(), order.PrivateStatus, int32(constant.OrderStatusNew))

	req1 := &grpc.PaymentFormJsonDataRequest{
		OrderId: order.Uuid,
		Scheme:  "https",
		Host:    "unit.test",
		Ip:      "127.0.0.1",
	}
	rsp := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp)

	assert.Nil(suite.T(), err)
	assert.True(suite.T(), len(rsp.Item.PaymentMethods) > 0)
	assert.True(suite.T(), len(rsp.Item.PaymentMethods[0].Id) > 0)
	assert.Len(suite.T(), rsp.Item.PaymentMethods, 1)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_NoPaymentMethods() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "KZT",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
			Address: &billing.OrderBillingAddress{
				Country: "RU",
			},
		},
	}

	suite.service.supportedCurrencies = append(suite.service.supportedCurrencies, "KZT")
	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item
	assert.NotNil(suite.T(), order.CountryRestriction)
	assert.Equal(suite.T(), order.CountryRestriction.IsoCodeA2, "RU")
	assert.True(suite.T(), order.CountryRestriction.PaymentsAllowed)
	assert.True(suite.T(), order.CountryRestriction.ChangeAllowed)
	assert.False(suite.T(), order.UserAddressDataRequired)
	assert.Equal(suite.T(), order.PrivateStatus, int32(constant.OrderStatusNew))

	req1 := &grpc.PaymentFormJsonDataRequest{
		OrderId: order.Uuid,
		Scheme:  "https",
		Host:    "unit.test",
		Ip:      "127.0.0.1",
	}
	rsp := &grpc.PaymentFormJsonDataResponse{}
	err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusNotFound, rsp.Status)
	assert.Equal(suite.T(), orderErrorPaymentMethodNotAllowed, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateByPaylink_Ok() {
	req := &billing.OrderCreateByPaylink{
		PaylinkId:   suite.paylink1.Id,
		PayerIp:     "127.0.0.1",
		IssuerUrl:   "http://localhost/referrer",
		UtmSource:   "unit-test-source",
		UtmMedium:   "unit-test-medium",
		UtmCampaign: "unit-test-campaign",
		IsEmbedded:  false,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateByPaylink(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	assert.NotNil(suite.T(), rsp.Item)
	assert.Equal(suite.T(), pkg.OrderTypeOrder, rsp.Item.Type)
	assert.Equal(suite.T(), rsp.Item.Issuer.ReferenceType, pkg.OrderIssuerReferenceTypePaylink)
	assert.Equal(suite.T(), rsp.Item.Issuer.Reference, suite.paylink1.Id)
	assert.False(suite.T(), rsp.Item.Issuer.Embedded)
	assert.Equal(suite.T(), rsp.Item.Issuer.Url, "http://localhost/referrer")
	assert.Equal(suite.T(), rsp.Item.Issuer.UtmSource, "unit-test-source")
	assert.Equal(suite.T(), rsp.Item.Issuer.UtmMedium, "unit-test-medium")
	assert.Equal(suite.T(), rsp.Item.Issuer.UtmCampaign, "unit-test-campaign")
	assert.Equal(suite.T(), rsp.Item.PrivateMetadata["PaylinkId"], suite.paylink1.Id)
	assert.Len(suite.T(), rsp.Item.Products, len(suite.productIds))
	assert.Len(suite.T(), rsp.Item.Items, len(suite.productIds))
	assert.Equal(suite.T(), rsp.Item.Project.Id, suite.paylink1.ProjectId)
	assert.Equal(suite.T(), rsp.Item.Project.MerchantId, suite.paylink1.MerchantId)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateByPaylink_Fail_Expired() {
	req := &billing.OrderCreateByPaylink{
		PaylinkId:   suite.paylink3.Id,
		PayerIp:     "127.0.0.1",
		IssuerUrl:   "http://localhost/referrer",
		UtmSource:   "unit-test-source",
		UtmMedium:   "unit-test-medium",
		UtmCampaign: "unit-test-medium",
		IsEmbedded:  false,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateByPaylink(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusGone)
	assert.Equal(suite.T(), rsp.Message, errorPaylinkExpired)
	assert.Nil(suite.T(), rsp.Item)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateByPaylink_Fail_Deleted() {
	req := &billing.OrderCreateByPaylink{
		PaylinkId:   suite.paylink2.Id,
		PayerIp:     "127.0.0.1",
		IssuerUrl:   "http://localhost/referrer",
		UtmSource:   "unit-test-source",
		UtmMedium:   "unit-test-medium",
		UtmCampaign: "unit-test-medium",
		IsEmbedded:  false,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateByPaylink(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusNotFound)
	assert.Equal(suite.T(), rsp.Message, errorPaylinkNotFound)
	assert.Nil(suite.T(), rsp.Item)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateByPaylink_Fail_NotFound() {
	req := &billing.OrderCreateByPaylink{
		PaylinkId:   primitive.NewObjectID().Hex(),
		PayerIp:     "127.0.0.1",
		IssuerUrl:   "http://localhost/referrer",
		UtmSource:   "unit-test-source",
		UtmMedium:   "unit-test-medium",
		UtmCampaign: "unit-test-medium",
		IsEmbedded:  false,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateByPaylink(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusNotFound)
	assert.Equal(suite.T(), rsp.Message, errorPaylinkNotFound)
	assert.Nil(suite.T(), rsp.Item)
}

func (suite *OrderTestSuite) TestOrder_PaymentCreateProcess_CostsNotFound_Error() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	createPaymentRequest := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         order.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.pmBitcoin2.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldEWallet:         "bitcoin_address",
		},
	}

	rsp := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), createPaymentRequest, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Empty(suite.T(), rsp.RedirectUrl)
	assert.Equal(suite.T(), orderErrorCostsRatesNotFound, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_PurchaseReceipt_Ok() {
	zap.ReplaceGlobals(suite.logObserver)
	postmarkBrokerMockFn := func(topicName string, payload proto.Message, t amqp.Table) error {
		msg := payload.(*postmarkSdrPkg.Payload)
		zap.L().Info("order_test", zap.String("url", msg.TemplateModel["url"]))

		return nil
	}
	postmarkBrokerMock := &mocks.BrokerInterface{}
	postmarkBrokerMock.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(postmarkBrokerMockFn, nil)
	suite.service.postmarkBroker = postmarkBrokerMock
	order := helperCreateAndPayOrder(suite.Suite, suite.service, 100, "RUB", "RU", suite.project, suite.paymentMethod)
	assert.NotNil(suite.T(), order)
	assert.Equal(suite.T(), order.ReceiptUrl, suite.service.cfg.GetReceiptPurchaseUrl(order.Uuid, order.ReceiptId))
	assert.Nil(suite.T(), order.Cancellation)

	messages := suite.zapRecorder.All()

	for _, v := range messages {
		if v.Entry.Message == "order_test" {
			assert.Equal(suite.T(), zapcore.InfoLevel, v.Level)
			assert.Equal(suite.T(), v.Context[0].String, order.ReceiptUrl)
		}
	}
}

func (suite *OrderTestSuite) TestOrder_RefundReceipt_Ok() {
	zap.ReplaceGlobals(suite.logObserver)
	postmarkBrokerMockFn := func(topicName string, payload proto.Message, t amqp.Table) error {
		msg := payload.(*postmarkSdrPkg.Payload)
		zap.L().Info("order_test_refund", zap.String("url", msg.TemplateModel["url"]))
		return nil
	}

	postmarkBrokerMock := &mocks.BrokerInterface{}
	postmarkBrokerMock.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(postmarkBrokerMockFn, nil)
	suite.service.postmarkBroker = postmarkBrokerMock

	order := helperCreateAndPayOrder(suite.Suite, suite.service, 100, "RUB", "RU", suite.project, suite.paymentMethod)
	assert.NotNil(suite.T(), order)
	assert.Equal(suite.T(), order.ReceiptUrl, suite.service.cfg.GetReceiptPurchaseUrl(order.Uuid, order.ReceiptId))
	assert.Nil(suite.T(), order.Cancellation)

	refund := helperMakeRefund(suite.Suite, suite.service, order, order.ChargeAmount, false)
	assert.NotNil(suite.T(), refund)

	order, err := suite.service.getOrderById(context.TODO(), order.Id)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)

	refundOrder, err := suite.service.getOrderById(context.TODO(), refund.CreatedOrderId)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), refundOrder)

	assert.Equal(suite.T(), suite.service.cfg.GetReceiptRefundUrl(refundOrder.Uuid, refundOrder.ReceiptId), refundOrder.ReceiptUrl)
	assert.Equal(suite.T(), suite.service.cfg.GetReceiptPurchaseUrl(order.Uuid, order.ReceiptId), order.ReceiptUrl)
	assert.Nil(suite.T(), order.Cancellation)

	messages := suite.zapRecorder.All()

	urlsSent := map[string]bool{
		refundOrder.ReceiptUrl: false,
		order.ReceiptUrl:       false,
	}
	for _, v := range messages {
		if v.Entry.Message != "order_test_refund" {
			continue
		}
		urlsSent[v.Context[0].String] = true
	}

	for key, v := range urlsSent {
		assert.True(suite.T(), v, key)
	}
}

func (suite *OrderTestSuite) TestOrder_DeclineOrder_Ok() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.project.Id,
		Amount:      100,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)

	expireYear := time.Now().AddDate(1, 0, 0)

	req1 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Item.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
		Ip: "127.0.0.1",
	}
	rsp1 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req1, rsp1)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)

	order, err := suite.service.getOrderById(context.TODO(), rsp.Item.Id)
	assert.NoError(suite.T(), err)
	suite.NotNil(suite.T(), order)
	assert.Empty(suite.T(), order.ReceiptUrl)

	req2 := &billing.CardPayPaymentCallback{
		PaymentMethod: suite.paymentMethod.ExternalId,
		CallbackTime:  time.Now().Format("2006-01-02T15:04:05Z"),
		MerchantOrder: &billing.CardPayMerchantOrder{
			Id:          order.Id,
			Description: order.Description,
		},
		CardAccount: &billing.CallbackCardPayBankCardAccount{
			Holder:             order.PaymentRequisites[pkg.PaymentCreateFieldHolder],
			IssuingCountryCode: "RU",
			MaskedPan:          order.PaymentRequisites[pkg.PaymentCreateFieldPan],
			Token:              primitive.NewObjectID().Hex(),
		},
		Customer: &billing.CardPayCustomer{
			Email:  order.User.Email,
			Ip:     order.User.Ip,
			Id:     order.ProjectAccount,
			Locale: "Europe/Moscow",
		},
		PaymentData: &billing.CallbackCardPayPaymentData{
			Id:            primitive.NewObjectID().Hex(),
			Amount:        order.TotalPaymentAmount,
			Currency:      order.Currency,
			Description:   order.Description,
			Is_3D:         true,
			Rrn:           primitive.NewObjectID().Hex(),
			Status:        pkg.CardPayPaymentResponseStatusDeclined,
			DeclineCode:   "00000001",
			DeclineReason: "some decline reason",
		},
	}

	buf, err := json.Marshal(req2)
	assert.Nil(suite.T(), err)

	paymentSystem, err := suite.service.paymentSystem.GetById(context.TODO(), suite.paymentMethod.PaymentSystemId)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), paymentSystem)
	paymentSystem.Handler = pkg.PaymentSystemHandlerCardPay
	err = suite.service.paymentSystem.Update(context.TODO(), paymentSystem)
	assert.NoError(suite.T(), err)

	hash := sha512.New()
	hash.Write([]byte(string(buf) + order.PaymentMethod.Params.SecretCallback))

	zap.ReplaceGlobals(suite.logObserver)
	centrifugoPublishMockFn := func(ctx context.Context, channel string, msg interface{}) error {
		zap.L().Info("order_test_centrifugo_payment_system_complete")
		return nil
	}
	centrifugoMock := &mocks.CentrifugoInterface{}
	centrifugoMock.On("GetChannelToken", mock.Anything, mock.Anything).Return("token")
	centrifugoMock.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(centrifugoPublishMockFn)
	suite.service.centrifugo = centrifugoMock

	req3 := &grpc.PaymentNotifyRequest{
		OrderId:   order.Id,
		Request:   buf,
		Signature: hex.EncodeToString(hash.Sum(nil)),
	}
	rsp3 := &grpc.PaymentNotifyResponse{}
	err = suite.service.PaymentCallbackProcess(context.TODO(), req3, rsp3)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.StatusOK, rsp3.Status)

	order, err = suite.service.getOrderById(context.TODO(), rsp.Item.Id)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Empty(suite.T(), order.ReceiptUrl)
	assert.NotNil(suite.T(), order.Cancellation)
	assert.Equal(suite.T(), order.Cancellation.Code, req2.PaymentData.DeclineCode)
	assert.Equal(suite.T(), order.Cancellation.Reason, req2.PaymentData.DeclineReason)

	messages := suite.zapRecorder.All()
	hasCentrifugoMessage := false

	for _, v := range messages {
		if v.Entry.Message == "order_test_centrifugo_payment_system_complete" {
			hasCentrifugoMessage = true
		}
	}
	assert.False(suite.T(), hasCentrifugoMessage)
}

func (suite *OrderTestSuite) TestOrder_SuccessOrderCentrifugoPaymentSystemError_Ok() {
	req := &billing.OrderCreateRequest{
		ProjectId:   suite.project.Id,
		Amount:      100,
		Currency:    "RUB",
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		Type: billing.OrderType_simple,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)

	expireYear := time.Now().AddDate(1, 0, 0)

	req1 := &grpc.PaymentCreateRequest{
		Data: map[string]string{
			pkg.PaymentCreateFieldOrderId:         rsp.Item.Uuid,
			pkg.PaymentCreateFieldPaymentMethodId: suite.paymentMethod.Id,
			pkg.PaymentCreateFieldEmail:           "test@unit.unit",
			pkg.PaymentCreateFieldPan:             "4000000000000002",
			pkg.PaymentCreateFieldCvv:             "123",
			pkg.PaymentCreateFieldMonth:           "02",
			pkg.PaymentCreateFieldYear:            expireYear.Format("2006"),
			pkg.PaymentCreateFieldHolder:          "Mr. Card Holder",
		},
		Ip: "127.0.0.1",
	}
	rsp1 := &grpc.PaymentCreateResponse{}
	err = suite.service.PaymentCreateProcess(context.TODO(), req1, rsp1)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)

	order, err := suite.service.getOrderById(context.TODO(), rsp.Item.Id)
	assert.NoError(suite.T(), err)
	suite.NotNil(suite.T(), order)
	assert.Empty(suite.T(), order.ReceiptUrl)

	req2 := &billing.CardPayPaymentCallback{
		PaymentMethod: suite.paymentMethod.ExternalId,
		CallbackTime:  time.Now().Format("2006-01-02T15:04:05Z"),
		MerchantOrder: &billing.CardPayMerchantOrder{
			Id:          order.Id,
			Description: order.Description,
		},
		CardAccount: &billing.CallbackCardPayBankCardAccount{
			Holder:             order.PaymentRequisites[pkg.PaymentCreateFieldHolder],
			IssuingCountryCode: "RU",
			MaskedPan:          order.PaymentRequisites[pkg.PaymentCreateFieldPan],
			Token:              primitive.NewObjectID().Hex(),
		},
		Customer: &billing.CardPayCustomer{
			Email:  order.User.Email,
			Ip:     order.User.Ip,
			Id:     order.ProjectAccount,
			Locale: "Europe/Moscow",
		},
		PaymentData: &billing.CallbackCardPayPaymentData{
			Id:          primitive.NewObjectID().Hex(),
			Amount:      order.TotalPaymentAmount,
			Currency:    order.Currency,
			Description: order.Description,
			Is_3D:       true,
			Rrn:         primitive.NewObjectID().Hex(),
			Status:      pkg.CardPayPaymentResponseStatusCompleted,
		},
	}

	buf, err := json.Marshal(req2)
	assert.Nil(suite.T(), err)

	paymentSystem, err := suite.service.paymentSystem.GetById(context.TODO(), suite.paymentMethod.PaymentSystemId)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), paymentSystem)
	paymentSystem.Handler = pkg.PaymentSystemHandlerCardPay
	err = suite.service.paymentSystem.Update(context.TODO(), paymentSystem)
	assert.NoError(suite.T(), err)

	hash := sha512.New()
	hash.Write([]byte(string(buf) + order.PaymentMethod.Params.SecretCallback))

	zap.ReplaceGlobals(suite.logObserver)
	centrifugoPublishMockFn := func(ctx context.Context, channel string, msg interface{}) error {
		zap.L().Info("order_test_centrifugo_payment_system_complete")
		return nil
	}
	centrifugoMock := &mocks.CentrifugoInterface{}
	centrifugoMock.On("GetChannelToken", mock.Anything, mock.Anything).Return("token")
	centrifugoMock.On("Publish", mock.Anything, mock.Anything, mock.Anything).Return(centrifugoPublishMockFn)
	suite.service.centrifugo = centrifugoMock

	req3 := &grpc.PaymentNotifyRequest{
		OrderId:   order.Id,
		Request:   buf,
		Signature: hex.EncodeToString(hash.Sum(nil)),
	}
	rsp3 := &grpc.PaymentNotifyResponse{}
	err = suite.service.PaymentCallbackProcess(context.TODO(), req3, rsp3)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.StatusOK, rsp3.Status)

	order, err = suite.service.getOrderById(context.TODO(), rsp.Item.Id)
	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), order)
	assert.Equal(suite.T(), order.ReceiptUrl, suite.service.cfg.GetReceiptPurchaseUrl(order.Uuid, order.ReceiptId))
	assert.Nil(suite.T(), order.Cancellation)

	messages := suite.zapRecorder.All()
	hasCentrifugoMessage := false

	for _, v := range messages {
		if v.Entry.Message == "order_test_centrifugo_payment_system_complete" {
			hasCentrifugoMessage = true
		}
	}
	assert.True(suite.T(), hasCentrifugoMessage)
}

func (suite *OrderTestSuite) TestOrder_KeyProductWithoutPriceDifferentRegion_Ok() {
	shouldBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		ProjectId:     suite.projectWithKeyProducts.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.2",
		},
		Products: suite.keyProductIds,
		Type:     billing.OrderType_key,
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	rsp2 := &grpc.ProcessBillingAddressResponse{}
	err = suite.service.ProcessBillingAddress(context.TODO(), &grpc.ProcessBillingAddressRequest{
		OrderId: order.Uuid,
		Country: "UA",
	}, rsp2)

	assert.NoError(suite.T(), err)
	shouldBe.Equal(rsp2.Item.Currency, "USD")
	for _, v := range rsp2.Item.Items {
		shouldBe.Equal(rsp2.Item.Currency, v.Currency)
	}
}

func (suite *OrderTestSuite) TestOrder_ProductWithoutPriceDifferentRegion_Ok() {
	shouldBe := require.New(suite.T())

	req := &billing.OrderCreateRequest{
		ProjectId:     suite.projectWithProducts.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Account:       "unit test",
		Description:   "unit test",
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.2",
		},
		Products: suite.productIds,
		Type:     billing.OrderType_product,
	}

	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
	order := rsp1.Item

	rsp2 := &grpc.ProcessBillingAddressResponse{}
	err = suite.service.ProcessBillingAddress(context.TODO(), &grpc.ProcessBillingAddressRequest{
		OrderId: order.Uuid,
		Country: "UA",
	}, rsp2)

	assert.NoError(suite.T(), err)
	shouldBe.Equal(rsp2.Item.Currency, "USD")
	for _, v := range rsp2.Item.Items {
		shouldBe.Equal(rsp2.Item.Currency, v.Currency)
	}
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcessVirtualCurrency_Ok() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_product,
		ProjectId:     suite.projectWithProductsInVirtualCurrency.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Account:       "unit test",
		Description:   "unit test",
		Products:      suite.productIdsWithVirtualCurrency,
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		IsBuyForVirtualCurrency: true,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp.Status, pkg.ResponseStatusOk)
	assert.True(suite.T(), len(rsp.Item.Id) > 0)
	assert.EqualValues(suite.T(), 130000, rsp.Item.OrderAmount)
	assert.Equal(suite.T(), "RUB", rsp.Item.Currency)
	assert.Equal(suite.T(), "virtual", rsp.Item.Items[0].Currency)
	assert.EqualValues(suite.T(), 100, rsp.Item.Items[0].Amount)
}

func (suite *OrderTestSuite) TestOrder_OrderCreateProcessVirtualCurrency_Fail() {
	req := &billing.OrderCreateRequest{
		Type:          billing.OrderType_product,
		ProjectId:     suite.projectWithProducts.Id,
		PaymentMethod: suite.paymentMethod.Group,
		Account:       "unit test",
		Description:   "unit test",
		Products:      suite.productIds,
		OrderId:       primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
		},
		IsBuyForVirtualCurrency: true,
	}

	rsp := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorVirtualCurrencyNotFilled, rsp.Message)
}

func (suite *OrderTestSuite) TestOrder_CreateOrderByTokenWithVirtualCurrency_Ok() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Email: &billing.TokenUserEmailValue{
				Value: "test@unit.test",
			},
			Phone: &billing.TokenUserPhoneValue{
				Value: "1234567890",
			},
			Name: &billing.TokenUserValue{
				Value: "Unit Test",
			},
			Ip: &billing.TokenUserIpValue{
				Value: "127.0.0.1",
			},
			Locale: &billing.TokenUserLocaleValue{
				Value: "ru",
			},
			Address: &billing.OrderBillingAddress{
				Country:    "RU",
				City:       "St.Petersburg",
				PostalCode: "190000",
				State:      "SPE",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:               suite.projectWithProductsInVirtualCurrency.Id,
			Description:             "test payment",
			Type:                    billing.OrderType_product,
			ProductsIds:             suite.productIdsWithVirtualCurrency,
			IsBuyForVirtualCurrency: true,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.Empty(suite.T(), rsp.Message)
	assert.NotEmpty(suite.T(), rsp.Token)

	req1 := &billing.OrderCreateRequest{
		Token: rsp.Token,
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err = suite.service.OrderCreateProcess(context.TODO(), req1, rsp0)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp0.Status, pkg.ResponseStatusOk)
	rsp1 := rsp0.Item
	assert.NotEmpty(suite.T(), rsp1.Id)
	assert.Equal(suite.T(), req.Settings.ProjectId, rsp1.Project.Id)
	assert.Equal(suite.T(), req.Settings.Description, rsp1.Description)
	assert.True(suite.T(), rsp1.IsBuyForVirtualCurrency)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_MinSystemLimitOk() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "EUR",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
			Address: &billing.OrderBillingAddress{
				Country: "RU",
			},
		},
	}

	suite.service.supportedCurrencies = append(suite.service.supportedCurrencies, "EUR")
	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusOk)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_LowerThanMinSystemLimit() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "EUR",
		Amount:      10,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
			Address: &billing.OrderBillingAddress{
				Country: "RU",
			},
		},
	}

	suite.service.supportedCurrencies = append(suite.service.supportedCurrencies, "EUR")
	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), orderErrorAmountLowerThanMinLimitSystem, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_PaymentFormJsonDataProcess_MinSystemLimitNotSet() {
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "DKK",
		Amount:      10,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
		User: &billing.OrderUser{
			Email: "test@unit.unit",
			Ip:    "127.0.0.1",
			Address: &billing.OrderBillingAddress{
				Country: "RU",
			},
		},
	}

	suite.service.supportedCurrencies = append(suite.service.supportedCurrencies, "DKK")
	rsp1 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp1)

	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), rsp1.Status, pkg.ResponseStatusBadData)
	assert.Equal(suite.T(), errorPaymentMinLimitSystemNotFound, rsp1.Message)
}

func (suite *OrderTestSuite) TestOrder_ReCreateOrder_Ok() {
	shouldBe := require.New(suite.T())
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	shouldBe.Nil(err)
	shouldBe.Equal(rsp0.Status, pkg.ResponseStatusOk)
	order := rsp0.Item

	allowedStatuses := []int32{
		constant.OrderStatusPaymentSystemRejectOnCreate,
		constant.OrderStatusPaymentSystemReject,
		constant.OrderStatusProjectReject,
		constant.OrderStatusPaymentSystemDeclined,
		constant.OrderStatusNew,
		constant.OrderStatusPaymentSystemCreate,
		constant.OrderStatusPaymentSystemCreate,
		constant.OrderStatusPaymentSystemCanceled,
	}

	for _, status := range allowedStatuses {
		order.PrivateStatus = status
		shouldBe.NoError(suite.service.updateOrder(ctx, order))

		rsp1 := &grpc.OrderCreateProcessResponse{}
		shouldBe.NoError(suite.service.OrderReCreateProcess(context.TODO(), &grpc.OrderReCreateProcessRequest{OrderId: order.GetUuid()}, rsp1))
		shouldBe.EqualValues(200, rsp1.Status)
		shouldBe.NotEqual(order.Id, rsp1.Item.Id)
		shouldBe.NotEqual(order.Uuid, rsp1.Item.Uuid)
		shouldBe.NotEqual(order.Status, rsp1.Item.Status)
		shouldBe.EqualValues(constant.OrderStatusNew, rsp1.Item.PrivateStatus)
		shouldBe.Empty(rsp1.Item.ReceiptUrl)
		shouldBe.NotEqual(order.ReceiptId, rsp1.Item.ReceiptId)

		req1 := &grpc.PaymentFormJsonDataRequest{OrderId: rsp1.Item.Uuid, Scheme: "https", Host: "unit.test",
			Ip: "127.0.0.1",
		}
		rsp2 := &grpc.PaymentFormJsonDataResponse{}
		err = suite.service.PaymentFormJsonDataProcess(context.TODO(), req1, rsp2)
		assert.Nil(suite.T(), err)
		assert.EqualValues(suite.T(), 200, rsp2.Status, rsp2.Message)
		assert.Equal(suite.T(), billing.OrderType_simple, rsp2.Item.Type)
		assert.NotNil(suite.T(), rsp2.Item.UserIpData)
	}

}

func (suite *OrderTestSuite) TestOrder_ReCreateOrder_Error() {
	shouldBe := require.New(suite.T())
	req := &billing.OrderCreateRequest{
		Type:        billing.OrderType_simple,
		ProjectId:   suite.project.Id,
		Currency:    "RUB",
		Amount:      100,
		Account:     "unit test",
		Description: "unit test",
		OrderId:     primitive.NewObjectID().Hex(),
	}

	rsp0 := &grpc.OrderCreateProcessResponse{}
	err := suite.service.OrderCreateProcess(context.TODO(), req, rsp0)

	shouldBe.Nil(err)
	shouldBe.Equal(rsp0.Status, pkg.ResponseStatusOk)
	order := rsp0.Item

	order.PrivateStatus = constant.OrderStatusPaymentSystemComplete
	shouldBe.NoError(suite.service.updateOrder(ctx, order))

	rsp1 := &grpc.OrderCreateProcessResponse{}
	shouldBe.NoError(suite.service.OrderReCreateProcess(context.TODO(), &grpc.OrderReCreateProcessRequest{OrderId: order.GetUuid()}, rsp1))
	shouldBe.EqualValues(400, rsp1.Status)
	shouldBe.NotNil(rsp1.Message)
}
