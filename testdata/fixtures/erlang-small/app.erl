%% Application module exercising a cross-module mod:func remote call.
-module(app).
-behaviour(shape).
-export([run/0]).

run() ->
    shape:area({circle, 2, "c"}).
