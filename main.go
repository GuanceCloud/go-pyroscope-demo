package main

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	otelpyroscope "github.com/grafana/otel-profiling-go"
	"github.com/grafana/pyroscope-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var UUID = uuid.NewString()
var otelTracer trace.Tracer

type OtelTraceWrapper struct {
	trace.Span
	SpanCtx          context.Context
	PreviousPProfCTx context.Context
}

func (d *OtelTraceWrapper) Finish() {
	d.Span.End()
	if d.PreviousPProfCTx != nil {
		pprof.SetGoroutineLabels(d.PreviousPProfCTx)
	}
}

// StartSpanFromContext otel span wrapper
func StartSpanFromContext(ctx context.Context, operationName string, opts ...trace.SpanStartOption) *OtelTraceWrapper {
	wrapper := new(OtelTraceWrapper)
	wrapper.SpanCtx, wrapper.Span = otelTracer.Start(ctx, operationName, opts...)
	wrapper.PreviousPProfCTx = ctx

	labeledCtx := pprof.WithLabels(ctx, pprof.Labels(
		"span_id", Number2String(wrapper.SpanContext().SpanID()),
		"trace_id", Number2String(wrapper.SpanContext().TraceID()),
		"operation_name", operationName,
		//"runtime-id", runtimeID,
	))
	pprof.SetGoroutineLabels(labeledCtx)

	return wrapper
}

const BaseServiceName = "go-pyroscope-demo"

//go:embed movies5000.json.gz
var moviesJSON []byte

var serviceId = func() *atomic.Int64 {
	return &atomic.Int64{}
}()

func resetServiceID() {
	serviceId.Store(0)
}

func getCurServID() string {
	return strconv.FormatInt(serviceId.Load(), 10)
}

func getNextServID() string {
	serviceId.Add(1)
	return getCurServID()
}

func getCurServName() string {
	return fmt.Sprintf("%s-%s", BaseServiceName, getCurServID())
}

func getNextServName() string {
	return fmt.Sprintf("%s-%s", BaseServiceName, getNextServID())
}

var movies = func() []Movie {
	movies, err := readMovies()
	if err != nil {
		panic(err)
	}
	return movies
}()

type Movie struct {
	Title       string  `json:"title"`
	VoteAverage float64 `json:"vote_average"`
	ReleaseDate string  `json:"release_date"`
}

func GetCallerFuncName() string {
	pcs := make([]uintptr, 1)
	if runtime.Callers(2, pcs) < 1 {
		return ""
	}
	frame, _ := runtime.CallersFrames(pcs).Next()

	base := filepath.Base(frame.Function)

	if strings.ContainsRune(base, '.') {
		return filepath.Ext(base)[1:]
	}
	return base
}

func readMovies() ([]Movie, error) {
	r, err := gzip.NewReader(bytes.NewReader(moviesJSON))
	if err != nil {
		return nil, fmt.Errorf("gzip new reader from *FILE fail: %w", err)
	}
	defer r.Close()

	var mov []Movie

	if err := json.NewDecoder(r).Decode(&mov); err != nil {
		return nil, fmt.Errorf("json unmarshal fail: %w", err)
	}

	return mov, nil
}

func sendHtmlRequest(ctx context.Context, bodyText string, servName string) {
	_, span := otelTracer.Start(ctx, GetCallerFuncName(), trace.WithAttributes(attribute.String("service", servName)))
	defer span.End()

	req, err := http.NewRequest(http.MethodGet, "https://tv189.com/", strings.NewReader(strings.Repeat(bodyText, 1000)))

	if err != nil {
		log.Println(err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println(err)
		return
	}

	log.Println("response length: ", len(body))
}

func fibonacci(ctx context.Context, n int, servName string) int {
	if n <= 2 {
		return 1
	}
	if n%31 == 0 {
		return fibonacciWithTrace(ctx, n-1, servName) + fibonacciWithTrace(ctx, n-2, servName)
	} else if n%37 == 0 {
		return fibonacciWithTrace(ctx, n-1, servName) + fibonacciWithTrace(ctx, n-2, servName)
	}
	return fibonacci(ctx, n-1, servName) + fibonacci(ctx, n-2, servName)
}

func Number2String(n any) string {
	return fmt.Sprintf("%v", n)
}

func fibonacciWithTrace(ctx context.Context, n int, servName string) int {
	newCtx, span := otelTracer.Start(ctx, GetCallerFuncName(), trace.WithAttributes(attribute.String("service", servName)))
	defer span.End()
	return fibonacci(newCtx, n-1, servName) + fibonacci(newCtx, n-2, servName)
}

func httpReqWithTrace(ctx context.Context) {
	newCtx, span := otelTracer.Start(ctx, GetCallerFuncName(), trace.WithAttributes(attribute.String("service", getNextServName())))
	defer span.End()

	bodyText := `
黄河远上白云间，一片孤城万仞山。
羌笛何须怨杨柳，春风不度玉门关。
少小离家老大回，乡音无改鬓毛衰。
儿童相见不相识，笑问客从何处来。
`

	for i := 0; i < 10; i++ {
		sendHtmlRequest(newCtx, bodyText, getCurServName())
	}
}

func startPyroscope() (*pyroscope.Profiler, error) {
	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(5)

	hostname, _ := os.Hostname()

	p, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: "go-pyroscope-demo",

		// replace this with the address of pyroscope server
		ServerAddress: "http://127.0.0.1:9529",

		// you can disable logging by setting this to nil
		Logger: pyroscope.StandardLogger,

		// uploading interval period
		UploadRate: time.Minute,

		// you can provide static tags via a map:
		Tags: map[string]string{
			"service":    "go-pyroscope-demo",
			"env":        "demo",
			"version":    "0.0.1",
			"host":       hostname,
			"process_id": strconv.Itoa(os.Getpid()),
			"runtime_id": UUID,
		},

		ProfileTypes: []pyroscope.ProfileType{
			// these profile types are enabled by default:
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			// these profile types are optional:
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to bootstrap pyroscope profiler: %w", err)
	}

	return p, nil
}

func main() {

	os.Setenv("DD_TRACE_ENABLED", "false")
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.Baggage{},
		propagation.TraceContext{},
	)
	otel.SetTextMapPropagator(propagator)

	var UUID = uuid.NewString()

	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL("http://127.0.0.1:9529/otel/v1/trace"),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithTimeout(time.Second*15),
	)
	if err != nil {
		log.Fatal("unable to init otel tracing exporter: ", err)
	}
	provider := tracesdk.NewTracerProvider(tracesdk.WithBatcher(exporter,
		tracesdk.WithBatchTimeout(time.Second*3)),
		tracesdk.WithResource(resource.NewSchemaless(
			attribute.String("runtime_id", UUID),
			attribute.String("service.name", "go-pyroscope-demo"),
			attribute.String("service.version", "v0.0.1"),
			attribute.String("service.env", "dev"),
		)),
	)
	defer provider.Shutdown(context.Background())

	otel.SetTracerProvider(otelpyroscope.NewTracerProvider(provider))
	otelTracer = otel.Tracer("go-pyroscope-demo")
	log.Printf("otel tracing started....\n")

	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(5)
	hostname, _ := os.Hostname()

	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: "go-pyroscope-demo",

		// replace this with the address of pyroscope server
		ServerAddress: "http://127.0.0.1:9529",

		// you can disable logging by setting this to nil
		Logger: pyroscope.StandardLogger,

		// uploading interval period
		UploadRate: time.Minute,

		// you can provide static tags via a map:
		Tags: map[string]string{
			"service":    "go-pyroscope-demo",
			"env":        "demo",
			"version":    "0.0.1",
			"host":       hostname,
			"process_id": strconv.Itoa(os.Getpid()),
			"runtime_id": UUID,
		},

		ProfileTypes: []pyroscope.ProfileType{
			// these profile types are enabled by default:
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			// these profile types are optional:
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
	if err != nil {
		log.Fatal("unable to bootstrap pyroscope profiler: ", err)
	}
	defer profiler.Stop()
	log.Printf("pyroscope profiler started....\n")

	router := gin.New()
	//router.Use(gintrace.Middleware("go-pyroscope-demo"))

	// Access-Control-*
	router.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowCredentials: true,
		AllowHeaders:     []string{"*"},
		MaxAge:           time.Hour * 24,
	}))

	router.GET("/movies", func(ctx *gin.Context) {
		resetServiceID()

		newCtx := otel.GetTextMapPropagator().Extract(ctx.Request.Context(), propagation.HeaderCarrier(ctx.Request.Header))

		spanCtx, span := otelTracer.Start(newCtx, "/movies")
		defer span.End()

		var wg sync.WaitGroup
		wg.Add(2)

		go func(ctx context.Context) {

			defer wg.Done()
			param := 42
			log.Printf("fibonacci(%d) = %d\n", param, fibonacci(ctx, param, getNextServName()))
		}(spanCtx)

		go func(ctx context.Context) {
			defer wg.Done()
			httpReqWithTrace(ctx)
		}(spanCtx)

		q := ctx.Request.FormValue("q")

		moviesCopy := make([]Movie, len(movies))
		copy(moviesCopy, movies)

		//func() {
		//	request, err := http.NewRequestWithContext(tracer.ContextWithSpan(ctx.Request.Context(), span),
		//		http.MethodPost, "http://127.0.0.1:5888/foobar", nil)
		//	if err != nil {
		//		log.Println("unable to new request: ", err)
		//		return
		//	}
		//	err = tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(request.Header))
		//	if err != nil {
		//		log.Println("unable to inject span to request: ", err)
		//		return
		//	}
		//	resp, err := http.DefaultClient.Do(request)
		//	if err != nil {
		//		log.Println("unable to request go-http-client")
		//		return
		//	}
		//	defer resp.Body.Close()
		//
		//	body, err := io.ReadAll(resp.Body)
		//	if err != nil {
		//		log.Println("unable to read request body: ", err)
		//	}
		//
		//	fmt.Println("response: ", string(body))
		//}()

		sort.Slice(moviesCopy, func(i, j int) bool {
			time.Sleep(time.Microsecond * 10)
			t1, err := time.Parse("2006-01-02", moviesCopy[i].ReleaseDate)
			if err != nil {
				return false
			}
			t2, err := time.Parse("2006-01-02", moviesCopy[j].ReleaseDate)
			if err != nil {
				return true
			}
			return t1.After(t2)
		})

		if q != "" {
			q = strings.ToUpper(q)
			matchCount := 0
			for idx, m := range moviesCopy {
				if strings.Contains(strings.ToUpper(m.Title), q) && idx != matchCount {
					moviesCopy[matchCount] = moviesCopy[idx]
					matchCount++
				}
			}
			moviesCopy = moviesCopy[:matchCount]
		}

		encoder := json.NewEncoder(ctx.Writer)
		if err := encoder.Encode(moviesCopy); err != nil {
			log.Printf("encode into json fail: %s", err)
			ctx.Writer.WriteHeader(http.StatusInternalServerError)
		}
		wg.Wait()
	})

	if err := http.ListenAndServe(":8080", router); err != nil {
		log.Fatal(err)
	}
}
