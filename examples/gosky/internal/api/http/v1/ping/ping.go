package ping

import (
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func PingHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		ctx.JSON(200, app.Ping())
	}
}
