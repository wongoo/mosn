package sds

import (
	"errors"
	"sync"

	"mosn.io/mosn/pkg/log"
	"mosn.io/mosn/pkg/types"
	"mosn.io/pkg/utils"
)

type SdsClientImpl struct {
	SdsCallbackMap map[string]types.SdsUpdateCallbackFunc
	updatedLock    sync.Mutex
	sdsSubscriber  *SdsSubscriber
}

var sdsClient *SdsClientImpl
var sdsClientLock sync.Mutex
var sdsPostCallback func() = nil

var ErrSdsClientNotInit = errors.New("sds client not init")

// TODO: support sds client index instead of singleton
func NewSdsClientSingleton(cfg interface{}) types.SdsClient {
	sdsClientLock.Lock()
	defer sdsClientLock.Unlock()

	if sdsClient != nil {
		// update sds config
		sdsClient.sdsSubscriber.sdsConfig = cfg
		return sdsClient
	} else {
		sdsClient = &SdsClientImpl{
			SdsCallbackMap: make(map[string]types.SdsUpdateCallbackFunc),
		}
		// For Istio , sds config should be the same
		// So we use first sds config to init sds subscriber
		sdsClient.sdsSubscriber = NewSdsSubscriber(sdsClient, cfg)
		utils.GoWithRecover(sdsClient.sdsSubscriber.Start, nil)
		return sdsClient
	}
}

func CloseSdsClient() {
	sdsClientLock.Lock()
	defer sdsClientLock.Unlock()
	if sdsClient != nil && sdsClient.sdsSubscriber != nil {
		log.DefaultLogger.Warnf("[mtls] sds client stopped")
		sdsClient.sdsSubscriber.Stop()
		sdsClient.sdsSubscriber = nil
		sdsClient = nil
	}
}

func (client *SdsClientImpl) AddUpdateCallback(name string, callback types.SdsUpdateCallbackFunc) error {
	client.updatedLock.Lock()
	defer client.updatedLock.Unlock()
	client.SdsCallbackMap[name] = callback
	client.sdsSubscriber.SendSdsRequest(name)
	return nil
}

// DeleteUpdateCallback
func (client *SdsClientImpl) DeleteUpdateCallback(name string) error {
	client.updatedLock.Lock()
	defer client.updatedLock.Unlock()
	delete(client.SdsCallbackMap, name)
	return nil
}

// SetSecret invoked when sds subscriber get secret response
func (client *SdsClientImpl) SetSecret(name string, secret *types.SdsSecret) {
	client.updatedLock.Lock()
	defer client.updatedLock.Unlock()
	if fc, ok := client.SdsCallbackMap[name]; ok {
		log.DefaultLogger.Debugf("[xds] [sds client],set secret = %v", name)
		fc(name, secret)
	}
}

// SetPostCallback
func SetSdsPostCallback(fc func()) {
	sdsPostCallback = fc
}