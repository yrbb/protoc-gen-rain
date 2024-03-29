#!/bin/bash

PROJECT_ROOT=$1

if [ ! "$PROJECT_ROOT" ]; then
    PROJECT_ROOT=$(pwd)
fi

if [ ! -f $PROJECT_ROOT/go.mod ]; then
    echo "go.mod not exist"
    exit 1
fi

if [ ! -d $PROJECT_ROOT/proto ]; then
    echo "proto dir not exist"
    exit 1
fi

PROJECT_REPO=$(cat $PROJECT_ROOT/go.mod | grep "^module" | awk '{print $2}')/router

PROTO_PATH="$PROJECT_ROOT/proto"
ROUTER_PATH="$PROJECT_ROOT/router"

function gen_router(){
    for element in `ls $1`; do
        dir_or_file=$1"/"$element
        
        if [[ -d $dir_or_file ]]; then
            gen_router $dir_or_file
        elif [[ -f $dir_or_file ]] && [[ "${dir_or_file##*.}" == "proto" ]]; then
            protoc -I$PROTO_PATH \
                -I$GOPATH/src/github.com/grpc-ecosystem/grpc-gateway/third_party/googleapis \
                -I$GOPATH/src \
				-I/usr/local/include \
                --rain_out=repo=$PROJECT_REPO,path=$ROUTER_PATH:$ROUTER_PATH \
                $dir_or_file
        fi
    done
}

if [ -d $ROUTER_PATH ]; then
    rm -rf $ROUTER_PATH/*
else 
    mkdir $ROUTER_PATH
fi

mkdir $ROUTER_PATH/router

printf '{}' > $ROUTER_PATH/handler.json

gen_router $PROTO_PATH

protoc-gen-rain genhandler $PROJECT_REPO $ROUTER_PATH

# rm -rf $ROUTER_PATH/handler.json

printf '// Code generated by protoc-gen-rain. DO NOT EDIT.

package router

type Empty struct{}
' > $ROUTER_PATH/router/model.go

printf '// Code generated by protoc-gen-rain. DO NOT EDIT.

package router

import (
    "encoding/json"

	"github.com/gin-gonic/gin"
)

type Response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

func (s Response) String() string {
	bts, _ := json.Marshal(s)
	return string(bts)
}

func JSON(ctx *gin.Context, data any) {
	ctx.JSON(200, Response{
		Code: 0,
		Msg: "",
		Data: data,
	})
}

func Error(ctx *gin.Context, code int, err error) {
	ctx.JSON(200, Response{
		Code: code,
		Msg:  err.Error(),
		Data: nil,
	})
	ctx.Abort()
}

' > $ROUTER_PATH/router/response.go


printf '// Code generated by protoc-gen-rain. DO NOT EDIT.

package router

import (
	"fmt"
	"sync"

	"github.com/gin-gonic/gin"
)

var mIns sync.Map

func RegisterMiddleware(name string, handler gin.HandlerFunc) {
	mIns.Store(name, handler)
}

func Handle(g *gin.Engine, method, path string, middlewares []string, handler gin.HandlerFunc) {
	notFound := ""
	handlers := []gin.HandlerFunc{}

	for _, v := range middlewares {
		if h, ok := mIns.Load(v); ok {
			handlers = append(handlers, h.(gin.HandlerFunc))
		} else {
			notFound = v
			break
		}
	}

	if notFound != "" {
		g.Handle(method, path, func(ctx *gin.Context) {
			Error(ctx, 500, fmt.Errorf("middleware: %%s not found", notFound))
		})

		return
	}

	handlers = append(handlers, handler)

	g.Handle(method, path, handlers...)
}

' > $ROUTER_PATH/router/router.go
