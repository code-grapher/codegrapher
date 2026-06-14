# Application module exercising alias + cross-module Module.func calls.

defmodule App do
  alias Circle, as: C

  def run(radius) do
    shape = %C{radius: radius, name: "c"}
    a = Circle.area(shape)
    name = C.label(shape)
    {a, name}
  end
end
