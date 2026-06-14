function Get-Area {
    param($width, $height)
    return $width * $height
}

class Animal {
    [string]$Name

    [string] Speak() {
        return "..."
    }
}
