package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/wxio/grpcar/internal/proxy"

	// "github.com/mwitkow/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"github.com/wxio/grpcar/example/echosvc"
	pb "github.com/wxio/grpcar/example/proto/echo"
)

var (
	port    = flag.Int("port", 50051, "the port to serve on")
	proxyIt = flag.Bool("proxy", false, "proxy or echo service")

	errMissingMetadata = status.Errorf(codes.InvalidArgument, "missing metadata")
	errInvalidToken    = status.Errorf(codes.Unauthenticated, "invalid token")
)

// // var conn *grpc.ClientConn
// var sa = &grpcpool.ServiceArg{
// 	Service: "grpcpool",
// 	Target:  "localhost:50051",
// 	Opts: []grpc.DialOption{
// 		grpc.WithCodec(proxy.Codec()),
// 		grpc.WithInsecure(),
// 		// grpc.WithContextDialer(
// 		// 	func(ctx context.Context, addr string) (net.Conn, error) {
// 		// 		fmt.Printf("Dialing\n")
// 		// 		network, addr := parseDialTarget(addr)
// 		// 		return (&net.Dialer{}).DialContext(ctx, network, addr)
// 		// 	}),
// 	},
// }

func main() {
	flag.Parse()
	if *proxyIt {
		*port = *port + 1
	}
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	fmt.Printf("Listening on localhost:%d\n", *port)

	// err = grpcpool.Create(context.Background(), grpc.Dial, 1, 1000, *sa)
	// if err != nil {
	// 	log.Fatalf("gRPC.Create pool err: %v", err)
	// }

	director := func(ctx context.Context, fullMethodName string) (context.Context, *grpc.ClientConn, error) {
		var err error
		// conn, err := grpcpool.Get(context.Background(), sa.Service)
		conn, err := grpc.Dial("localhost:50051",
			grpc.WithCodec(proxy.Codec()),
			grpc.WithInsecure(),
		)
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			ctx = metadata.NewOutgoingContext(ctx, md.Copy())
		}
		// ctx = context.WithValue(ctx, sa, conn)
		return ctx, conn, err
	}

	// // A gRPC server with the proxying codec enabled.
	// server := grpc.NewServer(grpc.CustomCodec(proxy.Codec()))
	// Create tls based credential.
	// creds, err := credentials.NewServerTLSFromFile(testdata.Path("server1.pem"), testdata.Path("server1.key"))
	// if err != nil {
	// 	log.Fatalf("failed to create credentials: %v", err)
	// }
	svr := grpc.NewServer(

		grpc.CustomCodec(proxy.Codec()),
		// grpc.UnknownServiceHandler(proxy.TransparentHandler(director)),

		// grpc.Creds(creds),
		grpc.UnaryInterceptor(unaryInterceptor),
		grpc.StreamInterceptor(streamInterceptor),
	)

	if *proxyIt { // Register a TestService with 4 of its methods explicitly.
		proxy.RegisterService(svr, director,
			"wxio.grpcar.example.echo.Echo",
			"UnaryEcho", "ServerStreamingEcho", "ClientStreamingEcho", "BidirectionalStreamingEcho")
		proxy.RegisterService(svr, director,
			"grpc.reflection.v1alpha.ServerReflection",
			"ServerReflectionInfo",
		)

	} else {
		// Register EchoServer on the server.
		pb.RegisterEchoServer(svr, echosvc.New())
		// github.com/grpc/grpc-go/reflection/grpc_reflection_v1alpha/reflection.proto
		reflection.Register(svr)
	}
	if err := svr.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// parseDialTarget returns the network and address to pass to dialer
func parseDialTarget(target string) (net string, addr string) {
	net = "tcp"

	m1 := strings.Index(target, ":")
	m2 := strings.Index(target, ":/")

	// handle unix:addr which will fail with url.Parse
	if m1 >= 0 && m2 < 0 {
		if n := target[0:m1]; n == "unix" {
			net = n
			addr = target[m1+1:]
			return net, addr
		}
	}
	if m2 >= 0 {
		t, err := url.Parse(target)
		if err != nil {
			return net, target
		}
		scheme := t.Scheme
		addr = t.Path
		if scheme == "unix" {
			net = scheme
			if addr == "" {
				addr = t.Host
			}
			return net, addr
		}
	}

	return net, target
}

// logger is to mock a sophisticated logging system. To simplify the example, we just print out the content.
func logger(format string, a ...interface{}) {
	fmt.Printf("LOG:\t"+format+"\n", a...)
}

func unaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	fmt.Printf("ctx:%+v\nreq:%+v\ninfo:%+v\nhandler:%+v\n", ctx, req, info, handler)
	// authentication (token verification)
	md, ok := metadata.FromIncomingContext(ctx)
	fmt.Printf("--- md %v\n", md)
	for k, v := range md {
		fmt.Printf("md %s : %s\n", k, v)
	}
	if !ok {
		return nil, errMissingMetadata
	}
	// if !valid(md["authorization"]) {
	// 	return nil, errInvalidToken
	// }
	m, err := handler(ctx, req)
	if err != nil {
		logger("RPC failed with error %v", err)
	}
	// err = grpcpool.PutBack(ctx, sa.Service, ctx.Value(sa).(*grpc.ClientConn))
	return m, err
}

// wrappedStream wraps around the embedded grpc.ServerStream, and intercepts the RecvMsg and
// SendMsg method call.
type wrappedStream struct {
	grpc.ServerStream
}

func (w *wrappedStream) RecvMsg(m interface{}) error {
	logger("Receive a message (Type: %T) at %s", m, time.Now().Format(time.RFC3339))
	return w.ServerStream.RecvMsg(m)
}

func (w *wrappedStream) SendMsg(m interface{}) error {
	logger("Send a message (Type: %T) at %v", m, time.Now().Format(time.RFC3339))
	return w.ServerStream.SendMsg(m)
}

func newWrappedStream(s grpc.ServerStream) grpc.ServerStream {
	return &wrappedStream{s}
}

func parseFullMethod(fm string) (pkg, svc, method string) {
	smIdx := strings.LastIndex(fm, "/")
	psIdx := strings.LastIndex(fm, ".")
	method = fm[smIdx+1:]
	pkg = fm[1:psIdx]
	svc = fm[psIdx+1 : smIdx]
	return
}

func streamInterceptor(
	srv interface{},
	svrs grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	fmt.Printf("srv:%+v\nserver stream:%+v\ninfo:%+v\nhandler:%+v\n", srv, svrs, info, handler)
	ctx := svrs.Context()
	// pkg, svc, _ := parseFullMethod(info.FullMethod)
	// mtype := proto.MessageType(pkg + "." + svc)
	// authentication (token verification)
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return errMissingMetadata
	}
	fmt.Printf("--- md %v\n", md)
	for k, v := range md {
		fmt.Printf("md %s : %s\n", k, v)
	}
	// if !valid(md["authorization"]) {
	// 	return errInvalidToken
	// }
	err := handler(srv, newWrappedStream(svrs))
	if err != nil {
		logger("RPC failed with error %v", err)
	}
	// err = grpcpool.PutBack(ctx, sa.Service, ctx.Value(sa).(*grpc.ClientConn))
	return err
}
