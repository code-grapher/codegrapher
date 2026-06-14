package com.example.shapes

abstract class Base(val name: String) : Shape {
    override fun label(): String {
        return name
    }
}
