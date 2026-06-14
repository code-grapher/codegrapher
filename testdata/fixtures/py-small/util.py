"""Utilities exercising decorators and nested functions."""

import functools


def trace(fn):
    """Decorator that returns the function unchanged."""

    @functools.wraps(fn)
    def wrapper(*args, **kwargs):
        return fn(*args, **kwargs)

    return wrapper


@trace
def compute(values):
    def total():
        return sum(values)

    return total()
