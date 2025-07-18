package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"github.com/spounge-ai/polykey-service/internal/server"
	"github.com/spounge-ai/polykey-service/internal/service"

	pk "github.com/spounge-ai/spounge-proto/gen/go/polykey/v2"
)

func loggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		startTime := time.Now()

		if info.FullMethod == "/grpc.health.v1.Health/Check" {
			return handler(ctx, req)
		}

		logger.Info("gRPC call received", "method", info.FullMethod)

		resp, err := handler(ctx, req)

		duration := time.Since(startTime)
		code := status.Code(err)
		logLevel := slog.LevelInfo
		if err != nil {
			logLevel = slog.LevelError
		}

		logger.Log(ctx, logLevel, "gRPC call finished",
			"method", info.FullMethod,
			"duration", duration.String(),
			"code", code.String(),
		)

		return resp, err
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":50051"
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	keepaliveParams := keepalive.ServerParameters{
		MaxConnectionIdle: 5 * time.Minute,
		Time:              2 * time.Hour,
		Timeout:           20 * time.Second,
	}

	srv := grpc.NewServer(
		grpc.KeepaliveParams(keepaliveParams),
		grpc.UnaryInterceptor(loggingInterceptor(logger)),
	)

	// Enable reflection for debugging
	reflection.Register(srv)

	healthSrv := health.NewServer()
	
	// Create service and server with correct interfaces
	polykeySvc := service.NewMockService()
	polykeyServer := server.NewServer(polykeySvc)

	// Register the service
	pk.RegisterPolykeyServiceServer(srv, polykeyServer)
	grpc_health_v1.RegisterHealthServer(srv, healthSrv)
	
	// Set health status
	healthSrv.SetServingStatus("polykey.v2.PolykeyService", grpc_health_v1.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Debug: Log registered services
	logger.Info("Registered services:")
	for serviceName, serviceInfo := range srv.GetServiceInfo() {
		logger.Info("Service registered", "name", serviceName, "methods", len(serviceInfo.Methods))
		for _, method := range serviceInfo.Methods {
			logger.Info("Method available", "service", serviceName, "method", method.Name)
		}
	}

	go func() {
		logger.Info("server starting", "address", addr)
		if err := srv.Serve(lis); err != nil {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("server shutting down")
	healthSrv.Shutdown()
	srv.GracefulStop()
	logger.Info("server stopped")
}