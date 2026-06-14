package com.example.shapes

class Circle(name: String, val radius: Double) extends Base(name) with Shape {
  override def area(): Double = Circle.Pi * radius * radius
}

object Circle {
  val Pi: Double = 3.14159

  def apply(radius: Double): Circle = new Circle("unit", radius)
}
