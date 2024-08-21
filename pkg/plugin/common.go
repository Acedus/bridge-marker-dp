package plugin

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	scheme = "unix"
)

type deviceHealth struct {
	DevId  string
	Health string
}

func SocketPath(deviceName string) string {
	return filepath.Join(pluginapi.DevicePluginPath, fmt.Sprintf("kubevirt-%s.sock", deviceName))
}

func waitForGRPCServer(socketPath string, timeout time.Duration) error {
	conn, err := gRPCConnect(socketPath, timeout)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func gRPCConnect(socketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	grpcPath := fmt.Sprintf("%s://%s", scheme, socketPath)
	conn, err := grpc.NewClient(grpcPath, grpc.WithTransportCredentials(insecure.NewCredentials()))

	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	defer cancel()

	for {
		state := conn.GetState()
		if state == connectivity.Idle {
			conn.Connect()
		}

		if state == connectivity.Ready {
			return conn, nil
		}

		if !conn.WaitForStateChange(ctx, state) {
			conn.Close()
			return nil, fmt.Errorf("Failed dial context at %s: %v", socketPath, ctx.Err())
		}
	}
}

func IsChanClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}
