. ./lib.ps1

class Dog : Animal {
    [string] Speak() {
        return "woof"
    }
}

function Invoke-Main {
    $area = Get-Area 3 4
    $d = [Dog]::new()
    $d.Speak()
    Write-Host $area
}
