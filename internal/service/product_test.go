package service

import (
	"context"
	"errors"
	"github.com/jinzhu/copier"
	casbinMocks "github.com/paysuper/casbin-server/pkg/mocks"
	"github.com/paysuper/paysuper-billing-server/internal/config"
	"github.com/paysuper/paysuper-billing-server/internal/mocks"
	"github.com/paysuper/paysuper-billing-server/pkg"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/billing"
	"github.com/paysuper/paysuper-billing-server/pkg/proto/grpc"
	reportingMocks "github.com/paysuper/paysuper-reporter/pkg/mocks"
	"github.com/stretchr/testify/assert"
	mock2 "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
	"gopkg.in/ProtocolONE/rabbitmq.v1/pkg"
	mongodb "gopkg.in/paysuper/paysuper-database-mongo.v1"
	"testing"
)

var (
	initialName = "Double Yeti"
	merchantId  = "5bdc35de5d1e1100019fb7db"
	projectId   = "5bdc35de5d1e1100019fb7db"
)

type ProductTestSuite struct {
	suite.Suite
	service *Service
	log     *zap.Logger
	cache   CacheInterface

	project    *billing.Project
	pmBankCard *billing.PaymentMethod
	product    *grpc.Product
	merchant   *billing.Merchant
}

func Test_Product(t *testing.T) {
	suite.Run(t, new(ProductTestSuite))
}

func (suite *ProductTestSuite) SetupTest() {
	cfg, err := config.NewConfig()
	assert.NoError(suite.T(), err, "Config load failed")

	db, err := mongodb.NewDatabase()
	if err != nil {
		suite.FailNow("Database connection failed", "%v", err)
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

	suite.log, err = zap.NewProduction()
	assert.NoError(suite.T(), err, "Logger initialization failed")

	broker, err := rabbitmq.NewBroker(cfg.BrokerAddress)
	assert.NoError(suite.T(), err, "Creating RabbitMQ publisher failed")

	redisdb := mocks.NewTestRedis()
	suite.cache, err = NewCacheRedis(redisdb, "cache")
	suite.service = NewBillingService(
		db,
		cfg,
		mocks.NewGeoIpServiceTestOk(),
		mocks.NewRepositoryServiceOk(),
		mocks.NewTaxServiceOkMock(),
		broker,
		nil,
		suite.cache,
		mocks.NewCurrencyServiceMockOk(),
		mocks.NewDocumentSignerMockOk(),
		&reportingMocks.ReporterService{},
		mocks.NewFormatterOK(),
		mocks.NewBrokerMockOk(),
		&casbinMocks.CasbinService{},
	)

	if err := suite.service.Init(); err != nil {
		suite.FailNow("Billing service initialization failed", "%v", err)
	}

	pgs := []*billing.PriceGroup{pgRub, pgUsd}
	if err := suite.service.priceGroup.MultipleInsert(context.TODO(), pgs); err != nil {
		suite.FailNow("Insert price group test data failed", "%v", err)
	}

	suite.merchant = &billing.Merchant{Id: primitive.NewObjectID().Hex(), Banking: &billing.MerchantBanking{Currency: "RUB"}}
	if err := suite.service.merchant.Insert(context.TODO(), suite.merchant); err != nil {
		suite.FailNow("Insert merchant test data failed", "%v", err)
	}

	suite.product = &grpc.Product{
		Object:          "product",
		Sku:             "ru_double_yeti",
		Name:            map[string]string{"en": initialName},
		DefaultCurrency: "USD",
		Enabled:         true,
		Description:     map[string]string{"en": "blah-blah-blah"},
		MerchantId:      primitive.NewObjectID().Hex(),
		ProjectId:       primitive.NewObjectID().Hex(),
		Prices: []*billing.ProductPrice{{
			Currency: "USD",
			Region:   "USD",
			Amount:   1005.00,
		}},
	}
}

func (suite *ProductTestSuite) TearDownTest() {
	err := suite.service.db.Drop()

	if err != nil {
		suite.FailNow("Database deletion failed", "%v", err)
	}

	err = suite.service.db.Close()

	if err != nil {
		suite.FailNow("Database close failed", "%v", err)
	}
}

func (suite *ProductTestSuite) TestProduct_GetProduct_Ok() {
	id := primitive.NewObjectID().Hex()
	merchantId := primitive.NewObjectID().Hex()

	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{MerchantId: merchantId}, nil)
	suite.service.productService = ps

	req := &grpc.RequestProduct{
		Id:         id,
		MerchantId: merchantId,
	}
	res := grpc.GetProductResponse{}
	err := suite.service.GetProduct(context.TODO(), req, &res)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusOk, res.Status)
	assert.Empty(suite.T(), res.Message)
	assert.NotNil(suite.T(), res.Item)
	assert.NotEmpty(suite.T(), res.Item.MerchantId)
}

func (suite *ProductTestSuite) TestProduct_GetProduct_Error_NotFound() {
	id := primitive.NewObjectID().Hex()
	merchantId := primitive.NewObjectID().Hex()

	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(nil, errors.New("not found"))
	suite.service.productService = ps

	req := &grpc.RequestProduct{
		Id:         id,
		MerchantId: merchantId,
	}
	res := grpc.GetProductResponse{}
	err := suite.service.GetProduct(context.TODO(), req, &res)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusNotFound, res.Status)
	assert.Equal(suite.T(), res.Message, productErrorNotFound)
}

func (suite *ProductTestSuite) TestProduct_GetProduct_Error_Merchant() {
	id := primitive.NewObjectID().Hex()
	merchantId := primitive.NewObjectID().Hex()

	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{MerchantId: merchantId}, nil)
	suite.service.productService = ps

	req := &grpc.RequestProduct{
		Id:         id,
		MerchantId: primitive.NewObjectID().Hex(),
	}
	res := grpc.GetProductResponse{}
	err := suite.service.GetProduct(context.TODO(), req, &res)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), pkg.ResponseStatusBadData, res.Status)
	assert.Equal(suite.T(), res.Message, productErrorMerchantNotEqual)
}

func (suite *ProductTestSuite) TestProduct_CreateProduct_DefaultPriceError() {
	res := grpc.Product{}
	err := suite.service.CreateOrUpdateProduct(context.TODO(), &grpc.Product{
		DefaultCurrency: "RUB",
		ProjectId:       suite.product.ProjectId,
		MerchantId:      suite.product.MerchantId,
		Prices: []*billing.ProductPrice{
			{Currency: "RUB", Amount: 100},
		},
		Sku:             "SKU",
		Object:          suite.product.Object,
		LongDescription: suite.product.LongDescription,
		Description:     suite.product.Description,
		Type:            suite.product.Type,
		Url:             suite.product.Url,
	}, &res)

	assert.NotNil(suite.T(), err)
	rspErr := err.(*grpc.ResponseErrorMessage)
	assert.EqualValues(suite.T(), "pd000010", rspErr.Code)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Ok_New() {
	res := grpc.Product{}
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), initialName, res.Name["en"])
	assert.Equal(suite.T(), 1, len(res.Prices))
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Ok_Exists() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{Sku: "ru_double_yeti", MerchantId: suite.product.MerchantId, ProjectId: suite.product.ProjectId}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	suite.product.Id = primitive.NewObjectID().Hex()
	suite.product.Sku = ""
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), initialName, res.Name["en"])
	assert.Equal(suite.T(), 1, len(res.Prices))
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_NotFound() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(nil, errors.New(""))
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	suite.product.Id = primitive.NewObjectID().Hex()
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorNotFound.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_MerchantNotEqual() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{Sku: "ru_double_yeti", MerchantId: primitive.NewObjectID().Hex()}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	suite.product.Id = primitive.NewObjectID().Hex()
	suite.product.Sku = ""
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorMerchantNotEqual.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_SkuNotEqual() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{MerchantId: primitive.NewObjectID().Hex()}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	suite.product.Id = primitive.NewObjectID().Hex()
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productSkuMismatch.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_ProjectNotEqual() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{Sku: "ru_double_yeti", MerchantId: suite.product.MerchantId, ProjectId: primitive.NewObjectID().Hex()}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	suite.product.Id = primitive.NewObjectID().Hex()
	suite.product.Sku = ""
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorProjectNotEqual.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_DefaultCurrency() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	suite.product.DefaultCurrency = ""
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorPriceDefaultCurrency.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_LocalizedName() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	suite.product.Name = map[string]string{"AZ": "test"}
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorNameDefaultLanguage.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_LocalizedDescription() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	suite.product.Description = map[string]string{"AZ": "test"}
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorDescriptionDefaultLanguage.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_Duplicates() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything, mock2.Anything).Return(int64(1), nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorProjectAndSkuAlreadyExists.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_CountByProjectSku() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything, mock2.Anything).Return(int64(0), errors.New(""))
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	res := grpc.Product{}
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorUnknown.Message)
}

func (suite *ProductTestSuite) TestProduct_CreateOrUpdateProduct_Error_Upsert() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("CountByProjectSku", mock2.Anything, mock2.Anything, mock2.Anything).Return(int64(0), nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(errors.New(""))
	suite.service.productService = ps

	res := grpc.Product{}
	err := suite.service.CreateOrUpdateProduct(context.TODO(), suite.product, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorUpsert.Message)
}

func (suite *ProductTestSuite) TestProduct_DeleteProduct_Error_NotFound() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(nil, errors.New(""))
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	req := grpc.RequestProduct{}
	res := grpc.EmptyResponse{}
	err := suite.service.DeleteProduct(context.TODO(), &req, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorNotFound.Message)
}

func (suite *ProductTestSuite) TestProduct_DeleteProduct_Error_MerchantNotEqual() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	req := grpc.RequestProduct{MerchantId: primitive.NewObjectID().Hex()}
	res := grpc.EmptyResponse{}
	err := suite.service.DeleteProduct(context.TODO(), &req, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorMerchantNotEqual.Message)
}

func (suite *ProductTestSuite) TestProduct_DeleteProduct_Error_Upsert() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(errors.New(""))
	suite.service.productService = ps

	req := grpc.RequestProduct{}
	res := grpc.EmptyResponse{}
	err := suite.service.DeleteProduct(context.TODO(), &req, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorDelete.Message)
}

func (suite *ProductTestSuite) TestProduct_DeleteProduct_Ok() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{}, nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	req := grpc.RequestProduct{}
	res := grpc.EmptyResponse{}
	err := suite.service.DeleteProduct(context.TODO(), &req, &res)

	assert.NoError(suite.T(), err)
}

func (suite *ProductTestSuite) TestProduct_ListProducts_Error() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("List", mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything).
		Return(int64(0), nil, errors.New(""))
	suite.service.productService = ps

	req := grpc.ListProductsRequest{}
	res := grpc.ListProductsResponse{}
	err := suite.service.ListProducts(context.TODO(), &req, &res)

	assert.NoError(suite.T(), err)
	assert.EqualValues(suite.T(), int32(0), res.Total)
	assert.Empty(suite.T(), res.Products)
}

func (suite *ProductTestSuite) TestProduct_ListProducts_Ok() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("List", mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything, mock2.Anything).
		Return(int64(1), []*grpc.Product{}, nil)
	suite.service.productService = ps

	req := grpc.ListProductsRequest{}
	res := grpc.ListProductsResponse{}
	err := suite.service.ListProducts(context.TODO(), &req, &res)

	assert.NoError(suite.T(), err)
}

func (suite *ProductTestSuite) TestProduct_GetProductPrices_Error_NotFound() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(nil, errors.New(""))
	suite.service.productService = ps

	req := grpc.RequestProduct{}
	res := grpc.ProductPricesResponse{}
	err := suite.service.GetProductPrices(context.TODO(), &req, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorNotFound.Message)
}

func (suite *ProductTestSuite) TestProduct_GetProductPrices_Ok() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{}, nil)
	suite.service.productService = ps

	req := grpc.RequestProduct{}
	res := grpc.ProductPricesResponse{}
	err := suite.service.GetProductPrices(context.TODO(), &req, &res)

	assert.NoError(suite.T(), err)
}

func (suite *ProductTestSuite) TestProduct_UpdateProductPrices_Error_EmptyPrices() {
	req := grpc.UpdateProductPricesRequest{}
	res := grpc.ResponseError{}
	err := suite.service.UpdateProductPrices(context.TODO(), &req, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorListPrices.Message)
}

func (suite *ProductTestSuite) TestProduct_UpdateProductPrices_Error_NotFound() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(nil, errors.New(""))
	suite.service.productService = ps

	req := grpc.UpdateProductPricesRequest{Prices: []*billing.ProductPrice{{Currency: "RUB"}}}
	res := grpc.ResponseError{}
	err := suite.service.UpdateProductPrices(context.TODO(), &req, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorNotFound.Message)
}

func (suite *ProductTestSuite) TestProduct_UpdateProductPrices_Error_DefaultCurrency() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{DefaultCurrency: "USD", MerchantId: suite.merchant.Id}, nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	req := grpc.UpdateProductPricesRequest{MerchantId: suite.merchant.Id, Prices: []*billing.ProductPrice{{Currency: "RUB"}}}
	res := grpc.ResponseError{}
	err := suite.service.UpdateProductPrices(context.TODO(), &req, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorPriceDefaultCurrency.Message)
}

func (suite *ProductTestSuite) TestProduct_UpdateProductPrices_Error_Upsert() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{DefaultCurrency: "RUB", MerchantId: suite.merchant.Id}, nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(errors.New(""))
	suite.service.productService = ps

	req := grpc.UpdateProductPricesRequest{MerchantId: suite.merchant.Id, Prices: []*billing.ProductPrice{{Currency: "RUB", Region: "RUB"}}}
	res := grpc.ResponseError{}
	err := suite.service.UpdateProductPrices(context.TODO(), &req, &res)

	assert.Error(suite.T(), err)
	assert.EqualError(suite.T(), err, productErrorPricesUpdate.Message)
}

func (suite *ProductTestSuite) TestProduct_UpdateProductPrices_Ok() {
	ps := &mocks.ProductServiceInterface{}
	ps.On("GetById", mock2.Anything, mock2.Anything).Return(&grpc.Product{DefaultCurrency: "RUB", MerchantId: suite.merchant.Id}, nil)
	ps.On("Upsert", mock2.Anything, mock2.Anything).Return(nil)
	suite.service.productService = ps

	req := grpc.UpdateProductPricesRequest{MerchantId: suite.merchant.Id, Prices: []*billing.ProductPrice{{Currency: "RUB", Region: "RUB"}}}
	res := grpc.ResponseError{}
	err := suite.service.UpdateProductPrices(context.TODO(), &req, &res)

	assert.NoError(suite.T(), err)

	req = grpc.UpdateProductPricesRequest{MerchantId: suite.merchant.Id, Prices: []*billing.ProductPrice{{IsVirtualCurrency: true}}}
	res = grpc.ResponseError{}
	err = suite.service.UpdateProductPrices(context.TODO(), &req, &res)

	assert.NoError(suite.T(), err)
}

func (suite *ProductTestSuite) TestProduct_Upsert_Ok() {
	err := suite.service.productService.Upsert(
		context.TODO(),
		&grpc.Product{
			Id:         primitive.NewObjectID().Hex(),
			ProjectId:  primitive.NewObjectID().Hex(),
			MerchantId: primitive.NewObjectID().Hex(),
		},
	)

	assert.NoError(suite.T(), err)
}

func (suite *ProductTestSuite) TestProduct_GetById_Ok() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	c, err := suite.service.productService.GetById(context.TODO(), suite.product.Id)

	assert.NoError(suite.T(), err)
	assert.NotNil(suite.T(), c)
	assert.Equal(suite.T(), suite.product.Id, c.Id)
}

func (suite *ProductTestSuite) TestProduct_GetById_Ok_ByCache() {
	ci := &mocks.CacheInterface{}
	ci.On("Get", "product:id:"+suite.product.Id, mock2.Anything).
		Return(nil)
	suite.service.cacher = ci
	c, err := suite.service.productService.GetById(context.TODO(), suite.product.Id)

	assert.NoError(suite.T(), err)
	assert.IsType(suite.T(), &grpc.Product{}, c)
}

func (suite *ProductTestSuite) TestProduct_GetById_Error_NotFound() {
	_, err := suite.service.productService.GetById(context.TODO(), primitive.NewObjectID().Hex())

	assert.Error(suite.T(), err)
}

func (suite *ProductTestSuite) TestProduct_CountByProjectSku_Ok() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	c, err := suite.service.productService.CountByProjectSku(context.TODO(), suite.product.ProjectId, suite.product.Sku)

	assert.NoError(suite.T(), err)
	assert.EqualValues(suite.T(), 1, c)
}

func (suite *ProductTestSuite) TestProduct_CountByProjectSku_NotMatch_Sku() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	c, err := suite.service.productService.CountByProjectSku(context.TODO(), suite.product.ProjectId, "")

	assert.NoError(suite.T(), err)
	assert.EqualValues(suite.T(), 0, c)
}

func (suite *ProductTestSuite) TestProduct_CountByProjectSku_NotMatch_Project() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	c, err := suite.service.productService.CountByProjectSku(context.TODO(), primitive.NewObjectID().Hex(), suite.product.Sku)

	assert.NoError(suite.T(), err)
	assert.EqualValues(suite.T(), 0, c)
}

func (suite *ProductTestSuite) TestProduct_List_Ok() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	count, list := suite.service.productService.List(context.TODO(), suite.product.MerchantId, "", "", "", 0, 10, 0)

	assert.EqualValues(suite.T(), int32(1), count)
	assert.Equal(suite.T(), suite.product.MerchantId, list[0].MerchantId)
	assert.Len(suite.T(), list, int(count))
}

func (suite *ProductTestSuite) TestProduct_List_Ok_Project() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	count, list := suite.service.productService.List(context.TODO(), suite.product.MerchantId, suite.product.ProjectId, "", "", 0, 10, 0)

	assert.EqualValues(suite.T(), int32(1), count)
	assert.Equal(suite.T(), suite.product.MerchantId, list[0].MerchantId)
	assert.Equal(suite.T(), suite.product.ProjectId, list[0].ProjectId)
	assert.Len(suite.T(), list, int(count))
}

func (suite *ProductTestSuite) TestProduct_List_Ok_Sku() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	count, list := suite.service.productService.List(context.TODO(), suite.product.MerchantId, "", suite.product.Sku, "", 0, 10, 0)

	assert.EqualValues(suite.T(), int32(1), count)
	assert.Equal(suite.T(), suite.product.MerchantId, list[0].MerchantId)
	assert.Equal(suite.T(), suite.product.Sku, list[0].Sku)
	assert.Len(suite.T(), list, int(count))
}

func (suite *ProductTestSuite) TestProduct_List_Ok_Name() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	count, list := suite.service.productService.List(context.TODO(), suite.product.MerchantId, "", "", suite.product.Name["en"], 0, 10, 0)

	assert.EqualValues(suite.T(), int32(1), count)
	assert.Equal(suite.T(), suite.product.MerchantId, list[0].MerchantId)
	assert.Equal(suite.T(), suite.product.Name, list[0].Name)
	assert.Len(suite.T(), list, int(count))
}

func (suite *ProductTestSuite) TestProduct_List_Error_Empty() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}

	count, list := suite.service.productService.List(context.TODO(), primitive.NewObjectID().Hex(), "", "", "", 0, 10, 0)
	assert.EqualValues(suite.T(), 0, count)
	assert.Empty(suite.T(), list)
}

func (suite *ProductTestSuite) TestProduct_List_Error_Offset() {
	suite.product.Id = primitive.NewObjectID().Hex()
	if err := suite.service.productService.Upsert(context.TODO(), suite.product); err != nil {
		suite.Assert().NoError(err)
	}
	count, list := suite.service.productService.List(context.TODO(), suite.product.MerchantId, "", "", "", 5, 10, 0)
	assert.EqualValues(suite.T(), 0, count)
	assert.Empty(suite.T(), list)
}

func (suite *ProductTestSuite) TestProduct_List_Enable_Ok() {
	for i := 0; i < 3; i++ {
		product := &grpc.Product{}
		err := copier.Copy(&product, &suite.product)
		assert.NoError(suite.T(), err)
		product.Id = primitive.NewObjectID().Hex()
		product.Enabled = true

		err = suite.service.productService.Upsert(context.TODO(), product)
		assert.NoError(suite.T(), err)
	}

	for i := 0; i < 5; i++ {
		product := &grpc.Product{}
		err := copier.Copy(&product, &suite.product)
		assert.NoError(suite.T(), err)
		product.Id = primitive.NewObjectID().Hex()
		product.Enabled = false

		err = suite.service.productService.Upsert(context.TODO(), product)
		assert.NoError(suite.T(), err)
	}

	count, list := suite.service.productService.List(context.TODO(), suite.product.MerchantId, "", "", "", 0, 10, 1)
	assert.EqualValues(suite.T(), 5, count)
	assert.Len(suite.T(), list, 5)

	count, list = suite.service.productService.List(context.TODO(), suite.product.MerchantId, "", "", "", 0, 10, 2)
	assert.EqualValues(suite.T(), 3, count)
	assert.Len(suite.T(), list, 3)
}
