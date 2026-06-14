# Domain protocol + a concrete shape for the elixir-small fixture.

@pi 3.14159

defprotocol Shape do
  @doc "Return the area of a shape."
  def area(shape)
end

defmodule Circle do
  defstruct [:radius, :name]

  def area(%Circle{radius: r}) do
    r * r * 3
  end

  def label(circle) do
    circle.name
  end

  defp scale(r), do: r * 2
end

defimpl Shape, for: Circle do
  def area(circle), do: Circle.area(circle)
end
