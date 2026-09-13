#!/usr/bin/env bash
set -Eeuo pipefail

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

jq -n '
  def threshold($n): if $n == 4 then 3 elif $n == 7 then 5 else 7 end;
  def schedule($phase; $warmups; $repetitions; $blocks):
    {phase:$phase,warmups:$warmups,repetitions:$repetitions,blocks:$blocks};
  {
    schema_version:"bloc-m5-scaling-matrix-v1",
    configuration:{
      execution_mode:"persistent",
      stream_mode:"persistent-lanes",
      echo_mode:"broadcast",
      acs_trace_enabled:false,
      selective_echo_enabled:false,
      seed:20260621,
      deadline:"12s",
      instance_type:"t3.small"
    },
    n10_capacity:{
      "three-region":{instances:11,vcpus_by_region:{"us-east-1":10,"eu-west-1":6,"eu-central-1":6}}
    },
    cells:[
      "three-region" as $topology |
      ([4, 7, 10][]) as $n |
      ([8, 32, 128, 512][]) as $batch |
      ($n < 10 and $batch < 512) as $primary |
      {
        id:"\($topology)-n\($n)-b\($batch)",
        topology:$topology,
        n:$n,
        threshold:threshold($n),
        batch:$batch,
        bmax:(if $batch == 512 then 512 else 128 end),
        classification:(if $primary then "replacement-primary" else "extension" end),
        profiles:{
          pilot:(if $primary then null else schedule("extension-pilot"; 5; 30; 3) end),
          full:(if $primary then schedule("latency"; 10; 1000; 10) else schedule("extension-full"; 10; 1000; 10) end),
          boundary:(if $primary then null else schedule("extension-boundary"; 10; 100; 10) end)
        }
      }
    ]
  }
'
