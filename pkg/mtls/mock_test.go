/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package mtls

import (
	"fmt"
	"net"
	"net/http"
	"testing"

	"mosn.io/mosn/pkg/config/v2"
	"mosn.io/mosn/pkg/module/http2"
	"mosn.io/mosn/pkg/mtls/certtool"
	"mosn.io/mosn/pkg/types"
)

type mockSdsClient struct {
	callback map[string]types.SdsUpdateCallbackFunc
}

var mockSdsClientInstance *mockSdsClient

func (c *mockSdsClient) AddUpdateCallback(name string, f types.SdsUpdateCallbackFunc) error {
	c.callback[name] = f
	return nil
}

func (c *mockSdsClient) SetSecret(name string, secret *types.SdsSecret) {
	if f, ok := c.callback[name]; ok {
		f(name, secret)
	}
}

func (c *mockSdsClient) DeleteUpdateCallback(name string) error {
	return nil
}

func getMockSdsClient(cfg interface{}) types.SdsClient {
	if mockSdsClientInstance == nil {
		mockSdsClientInstance = &mockSdsClient{
			callback: make(map[string]types.SdsUpdateCallbackFunc),
		}
	}
	return mockSdsClientInstance
}

// network io mock
type MockListener struct {
	net.Listener
	Mng types.TLSContextManager
}

func MockClient(t *testing.T, addr string, cltMng types.TLSClientContextManager) (*http.Response, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("request server error %v", err)
	}
	var conn net.Conn
	var req *http.Request
	conn = c
	if cltMng != nil {
		req, _ = http.NewRequest("GET", "https://"+addr, nil)
		conn, err = cltMng.Conn(c)
		if err != nil && cltMng.Fallback() {
			conn, err = net.Dial("tcp", addr)
		}
		if err != nil {
			return nil, fmt.Errorf("request tls handshake error %v", err)
		}
	} else {
		req, _ = http.NewRequest("GET", "http://"+addr, nil)
	}

	transport := &http2.Transport{}
	h2Conn, err := transport.NewClientConn(conn)
	return h2Conn.RoundTrip(req)
}

func (ln MockListener) Accept() (net.Conn, error) {
	conn, err := ln.Listener.Accept()
	if err != nil {
		return conn, err
	}
	conn, err = ln.Mng.Conn(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

type MockServer struct {
	Mng      types.TLSContextManager
	Addr     string
	server   *http2.Server
	t        *testing.T
	listener *MockListener
}

func (s *MockServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "mock server")
}

func (s *MockServer) GoListenAndServe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("listen failed %v", err)
		return
	}
	s.Addr = ln.Addr().String()
	s.listener = &MockListener{ln, s.Mng}
	go s.serve(s.listener)
}

func (s *MockServer) serve(ln *MockListener) {
	for {
		c, e := ln.Accept()
		if e != nil {
			return
		}

		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/", s.ServeHTTP)
			server := &http2.Server{}
			s.server = server
			if tlsConn, ok := c.(*TLSConn); ok {
				tlsConn.SetALPN(http2.NextProtoTLS)
				if err := tlsConn.Handshake(); err != nil {
					s.t.Logf("Hanshake failed, %v", err)
					return
				}
			}
			server.ServeConn(c, &http2.ServeConnOpts{
				Handler: mux,
			})
		}()
	}
}

func (s *MockServer) Close() {
	s.listener.Close()
}

type certInfo struct {
	CommonName string
	Curve      string
	DNS        string
}

func (c *certInfo) CreateSecret() (*secretInfo, error) {
	priv, err := certtool.GeneratePrivateKey(c.Curve)
	if err != nil {
		return nil, fmt.Errorf("generate key failed %v", err)
	}
	var dns []string
	if c.DNS != "" {
		dns = append(dns, c.DNS)
	}
	tmpl, err := certtool.CreateTemplate(c.CommonName, false, dns)
	if err != nil {
		return nil, fmt.Errorf("generate certificate template failed %v", err)
	}
	cert, err := certtool.SignCertificate(tmpl, priv)
	if err != nil {
		return nil, fmt.Errorf("sign certificate failed %v", err)
	}
	return &secretInfo{
		Certificate: cert.CertPem,
		PrivateKey:  cert.KeyPem,
		Validation:  certtool.GetRootCA().CertPem,
	}, nil
}

func (c *certInfo) CreateCertConfig() (*v2.TLSConfig, error) {
	secret, err := c.CreateSecret()
	if err != nil {
		return nil, err
	}
	return &v2.TLSConfig{
		Status:     true,
		CACert:     secret.Validation,
		CertChain:  secret.Certificate,
		PrivateKey: secret.PrivateKey,
	}, nil
}
