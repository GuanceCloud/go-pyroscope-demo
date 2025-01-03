# go-pyroscope-demo
One simple go app for [DataKit](https://www.guance.com) continuous profiling demonstrating

## 宿主机运行

### 构建

```shell
$ cd go-pyroscope-demo
$ go mod tidy
$ go build
```

### 运行

```shell
$ DD_TRACE_ENABLED=true \
DD_PROFILING_ENABLED=true \
DD_AGENT_HOST=127.0.0.1 \
DD_TRACE_AGENT_PORT=9529 \
DD_SERVICE=go-pyroscope-demo \
DD_ENV=demo \
DD_VERSION=v0.0.1 \
./go-pyroscope-demo
```

### 验证运行状态

```shell
$ curl 'http://localhost:8080/movies?q=batman'
[{"title":"Batman Begins","vote_average":4.6358799822491275,"release_date":"2005-05-31"},{"title":"Batman: Mystery of the Batwoman","vote_average":3.9549411967914105,"release_date":"2003-10-21"},{"title":"Batman Beyond: Return of the Joker","vote_average":1.8787282761678148,"release_date":"2000-10-31"},{"title":"Batman \u0026 Mr. Freeze: SubZero","vote_average":2.647401476348437,"release_date":"1998-03-17"},{"title":"Batman \u0026 Robin (film)","vote_average":2.7898866857094324,"release_date":"1997-06-12"},{"title":"Batman Forever","vote_average":2.4202373224443887,"release_date":"1995-06-09"},{"title":"Batman: Mask of the Phantasm","vote_average":4.107120385998093,"release_date":"1993-12-24"},{"title":"Batman Returns","vote_average":3.592054763414077,"release_date":"1992-06-16"},{"title":"Batman (1989 film)","vote_average":4.001755941748073,"release_date":"1989-06-19"},{"title":"Batman (1966 film)","vote_average":2.189099767926333,"release_date":"1966-07-30"},{"title":"Batman Fights Dracula","vote_average":3.863042623801402,"release_date":"1900-01-01"},{"title":"Batman Dracula","vote_average":4.483630276919295,"release_date":"1900-01-01"}]
```

如果安装了 `jq` 工具，可以对返回的json内容进行格式化

```shell
$ curl 'http://127.0.0.1:8080/movies?q=spider' | jq
[
  {
    "title": "Spider in the Web",
    "vote_average": 4.3551297815393175,
    "release_date": "2019-08-30"
  },
  {
    "title": "Spider-Man 3",
    "vote_average": 2.54672384115799,
    "release_date": "2007-04-16"
  },
  {
    "title": "Spider-Man 2",
    "vote_average": 2.7380715002602,
    "release_date": "2004-06-22"
  },
  {
    "title": "Spider (2002 film)",
    "vote_average": 2.1512631396751223,
    "release_date": "2002-12-13"
  },
  {
    "title": "Spider-Man (2002 film)",
    "vote_average": 2.666549403983728,
    "release_date": "2002-04-29"
  },
  {
    "title": "Kiss of the Spider Woman (film)",
    "vote_average": 2.3350488225969306,
    "release_date": "1985-05-13"
  },
  {
    "title": "Spider Baby",
    "vote_average": 0.33910029635005945,
    "release_date": "1900-01-01"
  },
  {
    "title": "Spiderweb (film)",
    "vote_average": 3.2936595576259915,
    "release_date": "1900-01-01"
  }
]
```

## Docker 下运行

```shell
$ docker build --build-arg DK_DATAWAY=<your-dataway-endpoint> -t go-pyroscope-demo .
$ docker run -d go-pyroscope-demo
```

> DK_DATAWAY可以从观测云空间 [集成 -> Datakit](https://console.guance.com/integration/datakit) 页面上复制，例如：
> docker build --build-arg DK_DATAWAY=https://openway.guance.com?token=tkn_f5b2989ba6ab44bc988cf7e2aa4a6de3 -t go-pyroscope-demo .


## 关联 Opentelemetry tracing 和 Pyroscope profiling

要在观测云实现 tracing 和 profiling 的关联，对于一个应用实例需要给 tracing 和 profiling 数据打上相同的 `runtime_id` 的 tag来方便两者的关联，例如，对于 golang 应用可以参考如下代码：

```go
package main

import (
	"context"
	_ "embed"
	"log"
	"os"
	"runtime"
	"strconv"
	"time"
	
	"github.com/google/uuid"
	otelpyroscope "github.com/grafana/otel-profiling-go"
	pyroscope "github.com/grafana/pyroscope-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)


func main() {
	var UUID = uuid.NewString() // 服务启动时生成一个唯一的 UUID，作为服务的唯一标识

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
			attribute.String("runtime_id", UUID), // 给 Opentelemetry 的 resource 打上 runtime_id
			attribute.String("service.name", "go-pyroscope-demo"),
			attribute.String("service.version", "v0.0.1"),
			attribute.String("service.env", "dev"),
		)),
	)
	defer provider.Shutdown(context.Background())

	otel.SetTracerProvider(otelpyroscope.NewTracerProvider(provider))

	......

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
			"runtime_id": UUID, // 给 Pyroscope 打上相同的 runtime_id
			"service":    "go-pyroscope-demo",
			"env":        "demo",
			"version":    "0.0.1",
			"host":       hostname,
			"process_id": strconv.Itoa(os.Getpid()),
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
}    
```

对于 JAVA 应用，我们可以使用环境变量 `OTEL_RESOURCE_ATTRIBUTES` 和 `PYROSCOPE_LABELS` 来分别注入 tracing 和 profiling 的 `runtime_id` tag，UUID 我们可以使用命令行工具 `uuidgen` 来生成，例如：

```shell
UUID=$(uuidgen); \
OTEL_SERVICE_NAME="java-pyro-demo" \
OTEL_RESOURCE_ATTRIBUTES="runtime_id=$UUID,service.name=java-pyro-demo,service.version=1.3.55,service.env=dev" \
OTEL_JAVAAGENT_EXTENSIONS=./pyroscope-otel.jar \
OTEL_TRACES_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_PROTOCOL="grpc" \
OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4317" \
PYROSCOPE_APPLICATION_NAME="java-pyro-demo" \
PYROSCOPE_LOG_LEVEL=debug \
PYROSCOPE_FORMAT="jfr" \
PYROSCOPE_PROFILER_EVENT="cpu" \
PYROSCOPE_LABELS="runtime_id=$UUID,service=java-pyro-demo,version=1.3.55,env=dev" \
PYROSCOPE_UPLOAD_INTERVAL="60s" \
PYROSCOPE_JAVA_STACK_DEPTH_MAX=512 \
PYROSCOPE_PROFILING_INTERVAL="10ms" \
PYROSCOPE_ALLOC_LIVE=false \
PYROSCOPE_SERVER_ADDRESS="http://127.0.0.1:9529" \
java -javaagent:opentelemetry-javaagent.jar -jar app.jar
```
其他编程语言可以参考上述设置方式，或参考 Opentelemetry 和 Pyroscope 的配置文档。 