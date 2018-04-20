/*
 * Tencent is pleased to support the open source community by making 蓝鲸 available.
 * Copyright (C) 2017-2018 THL A29 Limited, a Tencent company. All rights reserved.
 * Licensed under the MIT License (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 * http://opensource.org/licenses/MIT
 * Unless required by applicable law or agreed to in writing, software distributed under
 * the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ccapi

import (
	"configcenter/src/common/blog"
	"configcenter/src/common/core/cc/api"
	"configcenter/src/common/core/cc/config"
	"configcenter/src/common/http/httpserver"
	"configcenter/src/common/metric"
	"configcenter/src/common/types"
	confCenter "configcenter/src/scene_server/datacollection/datacollection/config"
	"configcenter/src/scene_server/datacollection/datacollection/rdiscover"
	"configcenter/src/source_controller/common/instdata"
	"time"
)

// CCAPIServer define data struct of bcs ccapi server
type CCAPIServer struct {
	conf     *config.CCAPIConfig
	httpServ *httpserver.HttpServer
	rd       *rdiscover.RegDiscover
	cfCenter *confCenter.ConfCenter
	httpheal bool
}

func NewCCAPIServer(conf *config.CCAPIConfig) (*CCAPIServer, error) {
	s := &CCAPIServer{}

	//config
	s.conf = conf
	addr, _ := s.conf.GetAddress()
	port, _ := s.conf.GetPort()

	//http server
	s.httpServ = httpserver.NewHttpServer(port, addr, "")

	a := api.NewAPIResource()
	a.SetConfig(s.conf)
	a.InitAction()

	s.rd = rdiscover.NewRegDiscover(s.conf.RegDiscover, addr, port, false)

	//ConfCenter
	s.cfCenter = confCenter.NewConfCenter(s.conf.RegDiscover)
	// MetricServer
	err := s.NewMetricServer(addr, port)
	return s, err
}

//Stop the ccapi server
func (ccAPI *CCAPIServer) Stop() error {
	return nil
}

//Start the ccapi server
func (ccAPI *CCAPIServer) Start() error {
	chErr := make(chan error, 3)

	a := api.NewAPIResource()

	// configure center
	if ccAPI.conf.ExConfig == "" {
		go func() {
			err := ccAPI.cfCenter.Start()
			blog.Errorf("configure center module start failed!. err:%s", err.Error())
			chErr <- err
		}()
	}

	/// fetch config of itselft
	var confData []byte
	var config map[string]string
	for {
		// temp code, just to debug
		if ccAPI.conf.ExConfig != "" {
			config, _ = a.ParseConfig()
			break
		}
		// end temp code
		confData = ccAPI.cfCenter.GetConfigureCxt()
		if confData == nil {
			blog.Warnf("fail to get configure, will get again")
			time.Sleep(time.Second * 2)
			continue
		} else {
			blog.Infof("get configure. ctx(%s)", string(confData))
			config, _ = a.ParseConf(confData)
			break
		}
	}

	//http server
	ccAPI.initHTTPServ()

	err := a.GetDataCli(config, "mongodb")
	if err != nil {
		blog.Error("connect mongodb error exit! err:%s", err.Error())
		chErr <- err
	}
	instdata.DataH = a.InstCli

	go func() {
		err := a.RunAutoAction(config)
		if err != nil {
			blog.Error("Run  auto execute action  error exit! err:%s", err.Error())
			chErr <- err
		}
	}()

	go func() {
		err := ccAPI.rd.Start()
		blog.Errorf("RegDiscover start failed! err:%s", err.Error())
		chErr <- err
	}()

	select {
	case err := <-chErr:
		blog.Error("exit! err:%s", err.Error())
		return err
	}

}

func (ccAPI *CCAPIServer) initHTTPServ() error {
	// a := api.NewAPIResource()
	// ccAPI.httpServ.RegisterWebServer("/datacollection/{version}", nil, a.Actions)
	return nil
}

func (ccAPI *CCAPIServer) NewMetricServer(ip string, port uint) error {
	conf := metric.Config{
		ModuleName: types.CC_MODULE_PROCCONTROLLER,
		IP:         ip,
		MetricPort: metric.MetricPort,
	}
	return metric.NewMetricController(conf, ccAPI.HealthMetric)
}

// HealthMetric check netservice is health
func (ccAPI *CCAPIServer) HealthMetric() metric.HealthMeta {

	meta := metric.HealthMeta{IsHealthy: true}
	a := api.GetAPIResource()

	// check mongo
	mongoHealthy := metric.HealthItem{Name: "mongo"}
	if err := a.InstCli.Ping(); err != nil {
		mongoHealthy.IsHealthy = false
		mongoHealthy.Message = err.Error()
	} else {
		mongoHealthy.IsHealthy = true
	}
	meta.Items = append(meta.Items, mongoHealthy)

	// check redis
	redisHealthy := metric.HealthItem{Name: "redis"}
	if err := a.CacheCli.Ping(); err != nil {
		redisHealthy.IsHealthy = false
		redisHealthy.Message = err.Error()
	} else {
		redisHealthy.IsHealthy = true
	}
	meta.Items = append(meta.Items, redisHealthy)

	// check http server
	httpHealthy := metric.HealthItem{Name: "http"}
	httpHealthy.IsHealthy = ccAPI.httpheal
	if ccAPI.httpheal {
		httpHealthy.Message = "listening on " + ccAPI.conf.AddrPort
	} else {
		httpHealthy.Message = "not listening http"
	}
	meta.Items = append(meta.Items, httpHealthy)

	for _, item := range meta.Items {
		if item.IsHealthy == false {
			meta.IsHealthy = false
			meta.Message = "proccontroller is not healthy"
			break
		}
	}

	return meta
}
