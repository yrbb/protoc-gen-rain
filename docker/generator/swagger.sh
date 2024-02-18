#!/bin/bash

PROTO_ROOT=$1 # proto文件目录地址（相对路径即可）
SWAGGER_DIR=$2 # swagger路径

fc=${PROTO_ROOT:0:1}
if [ "$fc" == "." ]; then
    PROTO_ROOT="$( pwd )"${PROTO_ROOT:1}
fi

function parseproto(){
    for element in `ls $1`; do
        dir_or_file=$1"/"$element

        if [[ -d $dir_or_file ]]; then
            parseproto $dir_or_file
        elif [[ -f $dir_or_file ]] && [[ "${dir_or_file##*.}" == "proto" ]]; then
          protoc  -I$PROTO_ROOT \
                  -I$GOPATH/src/github.com/grpc-ecosystem/grpc-gateway/third_party/googleapis \
                  -I$GOPATH/src \
                  -I/usr/local/include \
                  --swagger_out=json_names_for_fields=true,logtostderr=true:$SWAGGER_DIR \
                  $dir_or_file
        fi
    done
}

function swagger_plugins_pre_empty() {
  if ls $SWAGGER_DIR >/dev/null 2>&1; then
    rm -rf $SWAGGER_DIR/*
  fi
}

mkdir -p "$SWAGGER_DIR"

swagger_plugins_pre_empty
echo -e "\033[32m The swagger folder has been emptied! \033[0m"

parseproto "${PROTO_ROOT}"