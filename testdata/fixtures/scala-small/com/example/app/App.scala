package com.example.app

import com.example.shapes.Circle
import com.example.shapes.Shape

object Registry {
  def describe(s: Shape): String = s.label()
}

class App {
  def run(): Double = {
    val c = new Circle("unit", 1.0)
    val d = Circle(2.0)
    val a = c.area()
    a
  }
}
