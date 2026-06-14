package com.example.app

import com.example.shapes.Circle
import com.example.shapes.Shape

object Registry {
    fun describe(s: Shape): String {
        return s.label()
    }
}

fun Shape.scaledArea(factor: Double): Double {
    return area() * factor
}

fun makeUnit(): Circle {
    return Circle("unit", 1.0)
}

class App {
    fun run(): Double {
        val c = Circle("unit", 1.0)
        val a = c.area()
        return a
    }
}
