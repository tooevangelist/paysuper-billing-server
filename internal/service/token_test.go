package service

import (
	"context"
	"github.com/go-redis/redis"
	"github.com/golang/protobuf/ptypes"
	casbinMocks "github.com/paysuper/casbin-server/pkg/mocks"
	"github.com/paysuper/paysuper-billing-server/internal/config"
	"github.com/paysuper/paysuper-billing-server/internal/database"
	"github.com/paysuper/paysuper-billing-server/internal/mocks"
	"github.com/paysuper/paysuper-billing-server/pkg"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/billing"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/grpc"
	reportingMocks "github.com/paysuper/paysuper-reporter/pkg/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	mongodb "gopkg.in/paysuper/paysuper-database-mongo.v1"
	"net"
	"testing"
)

type TokenTestSuite struct {
	suite.Suite
	service *Service
	cache   CacheInterface

	project                            *billing.Project
	projectWithProducts                *billing.Project
	projectWithVirtualCurrencyProducts *billing.Project
	projectWithMerchantWithoutTariffs  *billing.Project

	product1    *grpc.Product
	product2    *grpc.Product
	keyProducts []*grpc.KeyProduct
	product3    *grpc.Product
}

func Test_Token(t *testing.T) {
	suite.Run(t, new(TokenTestSuite))
}

func (suite *TokenTestSuite) SetupTest() {
	cfg, err := config.NewConfig()
	assert.NoError(suite.T(), err, "Config load failed")

	db, err := mongodb.NewDatabase()
	assert.NoError(suite.T(), err, "Database connection failed")

	paymentMinLimitSystem1 := &billing.PaymentMinLimitSystem{
		Id:        primitive.NewObjectID().Hex(),
		Currency:  "RUB",
		Amount:    0.01,
		CreatedAt: ptypes.TimestampNow(),
		UpdatedAt: ptypes.TimestampNow(),
	}

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
	}
	us := &billing.Country{
		IsoCodeA2:       "US",
		Region:          "USD",
		Currency:        "USD",
		PaymentsAllowed: true,
		ChangeAllowed:   true,
		VatEnabled:      true,
		PriceGroupId:    pgRub.Id,
		VatCurrency:     "USD",
		VatThreshold: &billing.CountryVatThreshold{
			Year:  0,
			World: 0,
		},
		VatPeriodMonth:         3,
		VatDeadlineDays:        25,
		VatStoreYears:          5,
		VatCurrencyRatesPolicy: "last-day",
		VatCurrencyRatesSource: "cbrf",
	}

	merchant := &billing.Merchant{
		Id: primitive.NewObjectID().Hex(),
		Company: &billing.MerchantCompanyInfo{
			Name:               "Unit test",
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
		Status:                    pkg.MerchantStatusAgreementSigned,
		IsSigned:                  true,
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
	}
	merchantWithoutTariffs := &billing.Merchant{
		Id: primitive.NewObjectID().Hex(),
		Company: &billing.MerchantCompanyInfo{
			Name:               "Unit test",
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
		Status:                    pkg.MerchantStatusAgreementSigned,
		IsSigned:                  true,
	}

	project := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         pkg.ProjectCallbackProtocolEmpty,
		LimitsCurrency:           "RUB",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       false,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project 1 secret key",
		Status:                   pkg.ProjectStatusInProduction,
		MerchantId:               merchant.Id,
	}
	projectWithProducts := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         pkg.ProjectCallbackProtocolEmpty,
		LimitsCurrency:           "RUB",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       true,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project 1 secret key",
		Status:                   pkg.ProjectStatusInProduction,
		MerchantId:               merchant.Id,
	}
	projectWithMerchantWithoutTariffs := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         pkg.ProjectCallbackProtocolEmpty,
		LimitsCurrency:           "RUB",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       true,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project 1 secret key",
		Status:                   pkg.ProjectStatusInProduction,
		MerchantId:               merchantWithoutTariffs.Id,
	}
	projectWithVirtualCurrencyProducts := &billing.Project{
		Id:                       primitive.NewObjectID().Hex(),
		CallbackCurrency:         "RUB",
		CallbackProtocol:         pkg.ProjectCallbackProtocolEmpty,
		LimitsCurrency:           "RUB",
		MaxPaymentAmount:         15000,
		MinPaymentAmount:         1,
		Name:                     map[string]string{"en": "test project 1"},
		IsProductsCheckout:       true,
		AllowDynamicRedirectUrls: true,
		SecretKey:                "test project 1 secret key",
		Status:                   pkg.ProjectStatusInProduction,
		MerchantId:               merchant.Id,
		VirtualCurrency: &billing.ProjectVirtualCurrency{
			Name: map[string]string{"en": "test project 1"},
			Prices: []*billing.ProductPrice{
				{Amount: 100, Currency: "RUB", Region: "RUB"},
				{Amount: 10, Currency: "USD", Region: "USD"},
			},
		},
	}

	product3 := &grpc.Product{
		Id:              primitive.NewObjectID().Hex(),
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_double_yeti",
		Name:            map[string]string{"en": initialName},
		DefaultCurrency: "RUB",
		Enabled:         true,
		Description:     map[string]string{"en": "blah-blah-blah"},
		LongDescription: map[string]string{"en": "Super game steam keys"},
		Url:             "http://test.ru/dffdsfsfs",
		Images:          []string{"/home/image.jpg"},
		MerchantId:      projectWithVirtualCurrencyProducts.MerchantId,
		ProjectId:       projectWithVirtualCurrencyProducts.Id,
		Metadata: map[string]string{
			"SomeKey": "SomeValue",
		},
		Prices: []*billing.ProductPrice{{Amount: 10.00, IsVirtualCurrency: true}},
	}

	product1 := &grpc.Product{
		Id:              primitive.NewObjectID().Hex(),
		Object:          "product",
		Type:            "simple_product",
		Sku:             "ru_double_yeti",
		Name:            map[string]string{"en": initialName},
		DefaultCurrency: "RUB",
		Enabled:         true,
		Description:     map[string]string{"en": "blah-blah-blah"},
		LongDescription: map[string]string{"en": "Super game steam keys"},
		Url:             "http://test.ru/dffdsfsfs",
		Images:          []string{"/home/image.jpg"},
		MerchantId:      projectWithProducts.MerchantId,
		ProjectId:       projectWithProducts.Id,
		Metadata: map[string]string{
			"SomeKey": "SomeValue",
		},
		Prices: []*billing.ProductPrice{{Currency: "RUB", Amount: 1005.00, Region: "RUB"}},
	}
	product2 := &grpc.Product{
		Id:              primitive.NewObjectID().Hex(),
		Object:          "product1",
		Type:            "simple_product",
		Sku:             "ru_double_yeti1",
		Name:            map[string]string{"en": initialName},
		DefaultCurrency: "RUB",
		Enabled:         true,
		Description:     map[string]string{"en": "blah-blah-blah"},
		LongDescription: map[string]string{"en": "Super game steam keys"},
		Url:             "http://test.ru/dffdsfsfs",
		Images:          []string{"/home/image.jpg"},
		MerchantId:      projectWithProducts.MerchantId,
		ProjectId:       projectWithProducts.Id,
		Metadata: map[string]string{
			"SomeKey": "SomeValue",
		},
		Prices: []*billing.ProductPrice{{Currency: "RUB", Amount: 1005.00, Region: "RUB"}},
	}

	_, err = db.Collection(collectionProduct).InsertMany(context.TODO(), []interface{}{product1, product2, product3})
	assert.NoError(suite.T(), err, "Insert product test data failed")

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
		nil,
		nil,
		nil,
		redisClient,
		suite.cache,
		mocks.NewCurrencyServiceMockOk(),
		mocks.NewDocumentSignerMockOk(),
		&reportingMocks.ReporterService{},
		mocks.NewFormatterOK(),
		mocks.NewBrokerMockOk(),
		&casbinMocks.CasbinService{},
	)

	err = suite.service.Init()

	if err != nil {
		suite.FailNow("Billing service initialization failed", "%v", err)
	}

	limits := []interface{}{paymentMinLimitSystem1}
	_, err = suite.service.db.Collection(collectionPaymentMinLimitSystem).InsertMany(context.TODO(), limits)
	assert.NoError(suite.T(), err)

	err = suite.service.merchant.MultipleInsert(context.TODO(), []*billing.Merchant{merchant, merchantWithoutTariffs})

	if err != nil {
		suite.FailNow("Insert merchant test data failed", "%v", err)
	}

	projects := []*billing.Project{project, projectWithProducts, projectWithMerchantWithoutTariffs, projectWithVirtualCurrencyProducts}
	err = suite.service.project.MultipleInsert(context.TODO(), projects)

	if err != nil {
		suite.FailNow("Insert project test data failed", "%v", err)
	}

	err = suite.service.country.MultipleInsert(context.TODO(), []*billing.Country{ru, us})

	if err != nil {
		suite.FailNow("Insert country test data failed", "%v", err)
	}

	err = suite.service.priceGroup.MultipleInsert(context.TODO(), []*billing.PriceGroup{pgRub, pgUsd})

	if err != nil {
		suite.FailNow("Insert price group test data failed", "%v", err)
	}

	suite.project = project
	suite.projectWithProducts = projectWithProducts
	suite.projectWithVirtualCurrencyProducts = projectWithVirtualCurrencyProducts
	suite.projectWithMerchantWithoutTariffs = projectWithMerchantWithoutTariffs
	suite.product1 = product1
	suite.product2 = product2
	suite.product3 = product3

	suite.keyProducts = createKeyProductsForProject(suite.Suite, suite.service, suite.project, 3)
}

func (suite *TokenTestSuite) TearDownTest() {
	err := suite.service.db.Drop()

	if err != nil {
		suite.FailNow("Database deletion failed", "%v", err)
	}

	err = suite.service.db.Close()

	if err != nil {
		suite.FailNow("Database close failed", "%v", err)
	}
}

func (suite *TokenTestSuite) TestToken_CreateToken_NewCustomer_Ok() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Email: &billing.TokenUserEmailValue{
				Value:    "test@unit.test",
				Verified: true,
			},
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.project.Id,
			Amount:    100,
			Currency:  "RUB",
			Type:      billing.OrderType_simple,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.Empty(suite.T(), rsp.Message)
	assert.NotEmpty(suite.T(), rsp.Token)

	rep := &tokenRepository{
		service: suite.service,
		token:   &Token{},
	}
	err = rep.getToken(rsp.Token)
	assert.NoError(suite.T(), err)

	oid, err := primitive.ObjectIDFromHex(rep.token.CustomerId)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var customer *billing.Customer
	err = suite.service.db.Collection(collectionCustomer).FindOne(context.TODO(), filter).Decode(&customer)
	assert.NotNil(suite.T(), customer)

	assert.Equal(suite.T(), req.User.Id, customer.ExternalId)
	assert.Equal(suite.T(), customer.Id+pkg.TechEmailDomain, customer.TechEmail)
	assert.Equal(suite.T(), req.User.Email.Value, customer.Email)
	assert.Equal(suite.T(), req.User.Email.Verified, customer.EmailVerified)
	assert.Empty(suite.T(), customer.Phone)
	assert.False(suite.T(), customer.PhoneVerified)
	assert.Empty(suite.T(), customer.Name)
	assert.Empty(suite.T(), customer.Ip)
	assert.Equal(suite.T(), req.User.Locale.Value, customer.Locale)
	assert.Empty(suite.T(), customer.AcceptLanguage)
	assert.Empty(suite.T(), customer.UserAgent)
	assert.Nil(suite.T(), customer.Address)
	assert.Empty(suite.T(), customer.IpHistory)
	assert.Empty(suite.T(), customer.AddressHistory)
	assert.Empty(suite.T(), customer.AcceptLanguageHistory)
	assert.Empty(suite.T(), customer.Metadata)

	assert.Len(suite.T(), customer.Identity, 2)
	assert.Equal(suite.T(), customer.Identity[0].Value, customer.ExternalId)
	assert.True(suite.T(), customer.Identity[0].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypeExternal, customer.Identity[0].Type)
	assert.Equal(suite.T(), suite.project.Id, customer.Identity[0].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customer.Identity[0].MerchantId)

	assert.Equal(suite.T(), customer.Identity[1].Value, customer.Email)
	assert.True(suite.T(), customer.Identity[1].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypeEmail, customer.Identity[1].Type)
	assert.Equal(suite.T(), suite.project.Id, customer.Identity[1].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customer.Identity[1].MerchantId)
}

func (suite *TokenTestSuite) TestToken_CreateToken_ExistCustomer_Ok() {
	email := "test_exist_customer@unit.test"

	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Email: &billing.TokenUserEmailValue{
				Value:    email,
				Verified: true,
			},
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.project.Id,
			Amount:    100,
			Currency:  "RUB",
			Type:      billing.OrderType_simple,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.Empty(suite.T(), rsp.Message)
	assert.NotEmpty(suite.T(), rsp.Token)

	req.User.Phone = &billing.TokenUserPhoneValue{
		Value: "1234567890",
	}
	req.User.Email = &billing.TokenUserEmailValue{
		Value: "test_exist_customer_1@unit.test",
	}
	rsp1 := &grpc.TokenResponse{}
	err = suite.service.CreateToken(context.TODO(), req, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotEmpty(suite.T(), rsp1.Token)

	rep := &tokenRepository{
		service: suite.service,
		token:   &Token{},
	}
	err = rep.getToken(rsp.Token)
	assert.NoError(suite.T(), err)

	rep1 := &tokenRepository{
		service: suite.service,
		token:   &Token{},
	}
	err = rep1.getToken(rsp1.Token)
	assert.NoError(suite.T(), err)

	assert.Equal(suite.T(), rep.token.CustomerId, rep1.token.CustomerId)

	oid, err := primitive.ObjectIDFromHex(rep.token.CustomerId)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var customers []*billing.Customer
	cursor, err := suite.service.db.Collection(collectionCustomer).Find(context.TODO(), filter)
	assert.NoError(suite.T(), err)
	err = cursor.All(context.TODO(), &customers)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), customers, 1)

	assert.Len(suite.T(), customers[0].Identity, 4)
	assert.Equal(suite.T(), customers[0].Identity[3].Value, customers[0].Phone)
	assert.False(suite.T(), customers[0].Identity[3].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypePhone, customers[0].Identity[3].Type)
	assert.Equal(suite.T(), suite.project.Id, customers[0].Identity[3].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customers[0].Identity[3].MerchantId)

	assert.Equal(suite.T(), customers[0].Identity[2].Value, customers[0].Email)
	assert.False(suite.T(), customers[0].Identity[2].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypeEmail, customers[0].Identity[2].Type)
	assert.Equal(suite.T(), suite.project.Id, customers[0].Identity[2].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customers[0].Identity[2].MerchantId)

	assert.Equal(suite.T(), email, customers[0].Identity[1].Value)
	assert.True(suite.T(), customers[0].Identity[1].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypeEmail, customers[0].Identity[1].Type)
	assert.Equal(suite.T(), suite.project.Id, customers[0].Identity[1].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customers[0].Identity[1].MerchantId)

	assert.Equal(suite.T(), customers[0].Identity[0].Value, customers[0].ExternalId)
	assert.True(suite.T(), customers[0].Identity[0].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypeExternal, customers[0].Identity[0].Type)
	assert.Equal(suite.T(), suite.project.Id, customers[0].Identity[0].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customers[0].Identity[0].MerchantId)
}

func (suite *TokenTestSuite) TestToken_CreateToken_ExistCustomer_UpdateExistIdentity_Ok() {
	email := "test_exist_customer_update_exist_identity@unit.test"
	address := &billing.OrderBillingAddress{
		Country:    "UA",
		City:       "NewYork",
		PostalCode: "000000",
	}

	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Email: &billing.TokenUserEmailValue{
				Value: email,
			},
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
			Address: address,
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.project.Id,
			Amount:    100,
			Currency:  "RUB",
			Type:      billing.OrderType_simple,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.Empty(suite.T(), rsp.Message)
	assert.NotEmpty(suite.T(), rsp.Token)

	rep := &tokenRepository{
		service: suite.service,
		token:   &Token{},
	}
	err = rep.getToken(rsp.Token)
	assert.NoError(suite.T(), err)

	oid, err := primitive.ObjectIDFromHex(rep.token.CustomerId)
	assert.NoError(suite.T(), err)
	filter := bson.M{"_id": oid}

	var customer *billing.Customer
	err = suite.service.db.Collection(collectionCustomer).FindOne(context.TODO(), filter).Decode(&customer)
	assert.NotNil(suite.T(), customer)
	assert.False(suite.T(), customer.Identity[1].Verified)

	req.User.Phone = &billing.TokenUserPhoneValue{
		Value: "1234567890",
	}
	req.User.Email = &billing.TokenUserEmailValue{
		Value:    "test_exist_customer_update_exist_identity@unit.test",
		Verified: true,
	}
	req.User.Name = &billing.TokenUserValue{Value: "Unit test"}
	req.User.Ip = &billing.TokenUserIpValue{Value: "127.0.0.1"}
	req.User.Locale = &billing.TokenUserLocaleValue{Value: "ru"}
	req.User.Address = &billing.OrderBillingAddress{
		Country:    "RU",
		City:       "St.Petersburg",
		PostalCode: "190000",
	}
	rsp1 := &grpc.TokenResponse{}
	err = suite.service.CreateToken(context.TODO(), req, rsp1)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp1.Status)
	assert.Empty(suite.T(), rsp1.Message)
	assert.NotEmpty(suite.T(), rsp1.Token)

	rep = &tokenRepository{
		service: suite.service,
		token:   &Token{},
	}
	err = rep.getToken(rsp.Token)
	assert.NoError(suite.T(), err)

	rep1 := &tokenRepository{
		service: suite.service,
		token:   &Token{},
	}
	err = rep1.getToken(rsp.Token)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), rep.token.CustomerId, rep1.token.CustomerId)

	var customers []*billing.Customer
	cursor, err := suite.service.db.Collection(collectionCustomer).Find(context.TODO(), filter)
	assert.NoError(suite.T(), err)
	err = cursor.All(context.TODO(), &customers)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), customers, 1)

	assert.Len(suite.T(), customers[0].Identity, 3)
	assert.Equal(suite.T(), customers[0].Identity[2].Value, customers[0].Phone)
	assert.False(suite.T(), customers[0].Identity[2].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypePhone, customers[0].Identity[2].Type)
	assert.Equal(suite.T(), suite.project.Id, customers[0].Identity[2].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customers[0].Identity[2].MerchantId)

	assert.Equal(suite.T(), email, customers[0].Identity[1].Value)
	assert.True(suite.T(), customers[0].Identity[1].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypeEmail, customers[0].Identity[1].Type)
	assert.Equal(suite.T(), suite.project.Id, customers[0].Identity[1].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customers[0].Identity[1].MerchantId)

	assert.Equal(suite.T(), customers[0].Identity[0].Value, customers[0].ExternalId)
	assert.True(suite.T(), customers[0].Identity[0].Verified)
	assert.Equal(suite.T(), pkg.UserIdentityTypeExternal, customers[0].Identity[0].Type)
	assert.Equal(suite.T(), suite.project.Id, customers[0].Identity[0].ProjectId)
	assert.Equal(suite.T(), suite.project.MerchantId, customers[0].Identity[0].MerchantId)

	assert.Equal(suite.T(), req.User.Name.Value, customers[0].Name)
	assert.Equal(suite.T(), req.User.Ip.Value, net.IP(customers[0].Ip).String())
	assert.Equal(suite.T(), req.User.Locale.Value, customers[0].Locale)
	assert.Equal(suite.T(), req.User.Address, customers[0].Address)

	assert.Empty(suite.T(), customers[0].IpHistory)
	assert.NotEmpty(suite.T(), customers[0].LocaleHistory)
	assert.NotEmpty(suite.T(), customers[0].AddressHistory)

	assert.Equal(suite.T(), address.Country, customers[0].AddressHistory[0].Country)
	assert.Equal(suite.T(), address.City, customers[0].AddressHistory[0].City)
	assert.Equal(suite.T(), address.PostalCode, customers[0].AddressHistory[0].PostalCode)
}

func (suite *TokenTestSuite) TestToken_CreateToken_CustomerIdentityInformationNotFound_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.project.Id,
			Amount:    100,
			Currency:  "RUB",
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), tokenErrorUserIdentityRequired, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_ProjectNotFound_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: primitive.NewObjectID().Hex(),
			Amount:    100,
			Currency:  "RUB",
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorProjectNotFound, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_AmountIncorrect_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.project.Id,
			Amount:    -100,
			Currency:  "RUB",
			Type:      billing.OrderType_simple,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), tokenErrorSettingsSimpleCheckoutParamsRequired, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_ProjectIsProductCheckout_ProductInvalid_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.projectWithProducts.Id,
			ProductsIds: []string{primitive.NewObjectID().Hex(), primitive.NewObjectID().Hex(), primitive.NewObjectID().Hex()},
			Type:        billing.OrderType_product,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorProductsInvalid, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_ProjectIsProductCheckout_Ok() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.projectWithProducts.Id,
			ProductsIds: []string{suite.product1.Id, suite.product2.Id},
			Type:        billing.OrderType_product,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.Empty(suite.T(), rsp.Message)
	assert.NotEmpty(suite.T(), rsp.Token)

	rep := &tokenRepository{
		service: suite.service,
		token:   &Token{},
	}
	err = rep.getToken(rsp.Token)
	assert.NoError(suite.T(), err)

	assert.Len(suite.T(), rep.token.Settings.ProductsIds, 2)
}

func (suite *TokenTestSuite) TestToken_CreateToken_MerchantWithoutTariffs_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.projectWithMerchantWithoutTariffs.Id,
			Amount:    100,
			Currency:  "USD",
			Type:      billing.OrderType_simple,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorMerchantBadTariffs, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_ProductCheckout_ProductListEmpty_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.project.Id,
			Type:      billing.OrderType_product,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), tokenErrorSettingsProductAndKeyProductIdsParamsRequired, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_SimpleCheckout_InvalidCurrency_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.project.Id,
			Amount:    1000,
			Currency:  "KZT",
			Type:      billing.OrderType_simple,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorCurrencyNotFound, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_SimpleCheckout_LimitsAmount_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.project.Id,
			Amount:    0.1,
			Currency:  "RUB",
			Type:      billing.OrderType_simple,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorAmountLowerThanMinAllowed, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_KeyProductCheckout_Ok() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.project.Id,
			ProductsIds: []string{suite.keyProducts[0].Id, suite.keyProducts[1].Id},
			PlatformId:  "steam",
			Type:        billing.OrderType_key,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.Empty(suite.T(), rsp.Message)
	assert.NotEmpty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_KeyProductCheckout_ProjectWithoutKeyProducts_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.projectWithProducts.Id,
			ProductsIds: []string{suite.keyProducts[0].Id, suite.keyProducts[1].Id},
			PlatformId:  "steam",
			Type:        billing.OrderType_key,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), orderErrorProductsInvalid, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_UnknownType_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId: suite.projectWithProducts.Id,
			Type:      "unknown",
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), tokenErrorSettingsTypeRequired, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_KeyProductCheckout_WithAmount_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.project.Id,
			ProductsIds: []string{suite.keyProducts[0].Id, suite.keyProducts[1].Id},
			Type:        billing.OrderType_key,
			Amount:      100,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), tokenErrorSettingsAmountAndCurrencyParamNotAllowedForType, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_KeyProductCheckout_WithCurrency_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.project.Id,
			ProductsIds: []string{suite.keyProducts[0].Id, suite.keyProducts[1].Id},
			Type:        billing.OrderType_key,
			Currency:    "RUB",
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), tokenErrorSettingsAmountAndCurrencyParamNotAllowedForType, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_KeyProductCheckout_WithAmountAndCurrency_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.project.Id,
			ProductsIds: []string{suite.keyProducts[0].Id, suite.keyProducts[1].Id},
			Type:        billing.OrderType_key,
			Amount:      100,
			Currency:    "RUB",
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), tokenErrorSettingsAmountAndCurrencyParamNotAllowedForType, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_SimpleCheckout_WithProductIds_Error() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Email: &billing.TokenUserEmailValue{
				Value:    "test@unit.test",
				Verified: true,
			},
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:   suite.project.Id,
			Amount:      100,
			Currency:    "RUB",
			Type:        billing.OrderType_simple,
			ProductsIds: []string{suite.keyProducts[0].Id, suite.keyProducts[1].Id},
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, rsp.Status)
	assert.Equal(suite.T(), tokenErrorSettingsProductIdsParamNotAllowedForType, rsp.Message)
	assert.Empty(suite.T(), rsp.Token)
}

func (suite *TokenTestSuite) TestToken_CreateToken_ProjectWithVirtualCurrency_Ok() {
	req := &grpc.TokenRequest{
		User: &billing.TokenUser{
			Id: primitive.NewObjectID().Hex(),
			Locale: &billing.TokenUserLocaleValue{
				Value: "en",
			},
		},
		Settings: &billing.TokenSettings{
			ProjectId:               suite.projectWithVirtualCurrencyProducts.Id,
			ProductsIds:             []string{suite.product3.Id},
			Type:                    billing.OrderType_product,
			IsBuyForVirtualCurrency: true,
		},
	}
	rsp := &grpc.TokenResponse{}
	err := suite.service.CreateToken(context.TODO(), req, rsp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, rsp.Status)
	assert.Empty(suite.T(), rsp.Message)
	assert.NotEmpty(suite.T(), rsp.Token)

	rep := &tokenRepository{
		service: suite.service,
		token:   &Token{},
	}
	err = rep.getToken(rsp.Token)
	assert.NoError(suite.T(), err)

	assert.Len(suite.T(), rep.token.Settings.ProductsIds, 1)
	assert.True(suite.T(), rep.token.Settings.IsBuyForVirtualCurrency)
}
