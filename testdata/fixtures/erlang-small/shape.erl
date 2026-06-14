%% Domain module: a record + an exported area/1 function.
-module(shape).
-export([area/1]).

-record(circle, {radius, name}).

area(#circle{radius = R}) ->
    R * R * 3.
