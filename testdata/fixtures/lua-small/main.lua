-- main.lua — requires the shape module and exercises it.
local Shape = require("shape")

local function run()
  local s = Shape.new(3, 4)
  local a = Shape.area(s)
  local text = Shape.label(s)
  print(text)
  return a
end

run()
