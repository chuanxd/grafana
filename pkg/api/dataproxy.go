package api

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	// "github.com/Cepave/grafana/pkg/api/cloudwatch"
	"github.com/Cepave/grafana/pkg/bus"
	"github.com/Cepave/grafana/pkg/middleware"
	m "github.com/Cepave/grafana/pkg/models"
	"github.com/Cepave/grafana/pkg/setting"
	"github.com/Cepave/grafana/pkg/util"
)

var dataProxyTransport = &http.Transport{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	Proxy:           http.ProxyFromEnvironment,
	Dial: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).Dial,
	TLSHandshakeTimeout: 10 * time.Second,
}

/**
 * @function:		func NewReverseProxy(ds *m.DataSource, proxyPath string, targetUrl *url.URL) *httputil.ReverseProxy
 * @description:	This function initializes a reverse proxy.
 * @related issues:	OWL-159
 * @related issues:	OWL-168, OWL-123, OWL-028, OWL-017, OWL-002
 * @param:			ds *m.DataSource
 * @param:			proxyPath string
 * @param:			targetUrl *url.URL
 * @return:			*httputil.ReverseProxy
 * @author:			Don Hsieh
 * @since:			07/17/2015
 * @last modified: 	11/30/2015
 * @called by:		func ProxyDataSourceRequest(c *middleware.Context)
 *					 in pkg/api/dataproxy.go
 */
func NewReverseProxy(ds *m.DataSource, proxyPath string, targetUrl *url.URL) *httputil.ReverseProxy {
	director := func(req *http.Request) {
		req.URL.Scheme = targetUrl.Scheme
		req.URL.Host = targetUrl.Host
		req.Host = targetUrl.Host

		reqQueryVals := req.URL.Query()

		if ds.Type == m.DS_INFLUXDB_08 {
			req.URL.Path = util.JoinUrlFragments(targetUrl.Path, "db/"+ds.Database+"/"+proxyPath)
			reqQueryVals.Add("u", ds.User)
			reqQueryVals.Add("p", ds.Password)
			req.URL.RawQuery = reqQueryVals.Encode()
		} else if ds.Type == m.DS_INFLUXDB {
			req.URL.Path = util.JoinUrlFragments(targetUrl.Path, proxyPath)
			reqQueryVals.Add("db", ds.Database)
			req.URL.RawQuery = reqQueryVals.Encode()
			if !ds.BasicAuth {
				req.Header.Del("Authorization")
				req.Header.Add("Authorization", util.GetBasicAuthHeader(ds.User, ds.Password))
			}
		} else if ds.Type == "openfalcon" {
			reqQueryVals.Add("target", ds.Url)
			req.URL.RawQuery = reqQueryVals.Encode()

			proxyPath = "/"
			target, _ := url.Parse(ds.Url)
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			req.URL.Path = util.JoinUrlFragments(target.Path, proxyPath)
		} else {
			req.URL.Path = util.JoinUrlFragments(targetUrl.Path, proxyPath)
		}
		if ds.BasicAuth {
			req.Header.Del("Authorization")
			req.Header.Add("Authorization", util.GetBasicAuthHeader(ds.BasicAuthUser, ds.BasicAuthPassword))
		}

		// clear cookie headers
		req.Header.Del("Cookie")
		req.Header.Del("Set-Cookie")
	}

	return &httputil.ReverseProxy{Director: director}
}

var dsMap map[int64]*m.DataSource = make(map[int64]*m.DataSource)

func getDatasource(id int64, orgId int64) (*m.DataSource, error) {
	// ds, exists := dsMap[id]
	// if exists && ds.OrgId == orgId {
	// 	return ds, nil
	// }

	query := m.GetDataSourceByIdQuery{Id: id, OrgId: orgId}
	if err := bus.Dispatch(&query); err != nil {
		return nil, err
	}

	dsMap[id] = &query.Result
	return &query.Result, nil
}

func ProxyDataSourceRequest(c *middleware.Context) {
	ds, err := getDatasource(c.ParamsInt64(":id"), c.OrgId)
	if err != nil {
		c.JsonApiErr(500, "Unable to load datasource meta data", err)
		return
	}

	targetUrl, _ := url.Parse(ds.Url)
	if len(setting.DataProxyWhiteList) > 0 {
		if _, exists := setting.DataProxyWhiteList[targetUrl.Host]; !exists {
			c.JsonApiErr(403, "Data proxy hostname and ip are not included in whitelist", nil)
			return
		}
	}

	if ds.Type == m.DS_CLOUDWATCH {
		// cloudwatch.HandleRequest(c)
	} else {
		proxyPath := c.Params("*")
		proxy := NewReverseProxy(ds, proxyPath, targetUrl)
		proxy.Transport = dataProxyTransport
		proxy.ServeHTTP(c.RW(), c.Req.Request)
	}
}
